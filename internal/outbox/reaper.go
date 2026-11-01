package outbox

import (
	"context"
	"database/sql"
	"time"

	"github.com/redis/go-redis/v9"
)

// Reaper repairs work that a crashed or partitioned replica left behind. It is
// safe to run on every replica at once.
type Reaper struct {
	DB     *sql.DB
	Redis  *redis.Client
	Stream string
	Group  string
	Hooks  map[string]StateHook
	Logger Logger
	// RepublishAfter puts a published-but-unclaimed row back to pending. It must
	// comfortably exceed the time MaxInFlight messages need to drain.
	RepublishAfter time.Duration
}

func (r *Reaper) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.ReapOnce(ctx); err != nil && ctx.Err() == nil {
			r.Logger.PrintError(err, map[string]string{"component": "outbox-reaper"})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Reaper) ReapOnce(ctx context.Context) error {
	if err := r.expireLeases(ctx); err != nil {
		return err
	}
	if _, err := r.DB.ExecContext(ctx, `UPDATE outbox_messages SET status = 'pending', published_at = NULL
		WHERE status = 'published' AND published_at < NOW() - make_interval(secs => $1)`,
		r.RepublishAfter.Seconds()); err != nil {
		return err
	}
	r.cleanStream(ctx)
	return r.recordDepth(ctx)
}

// expireLeases handles consumers that died mid-send. The attempt they consumed
// still counts, so a message that crashes the worker cannot loop forever.
func (r *Reaper) expireLeases(ctx context.Context) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `UPDATE outbox_messages
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
			next_attempt_at = NOW(), locked_until = NULL, locked_by = NULL,
			last_error = COALESCE(last_error, 'worker lease expired')
		WHERE status = 'sending' AND locked_until < NOW()
		RETURNING id, topic, aggregate_id, campaign_id, attempts, max_attempts, status`)
	if err != nil {
		return err
	}
	type reclaimed struct {
		msg    Message
		status string
	}
	var expired []reclaimed
	for rows.Next() {
		var item reclaimed
		if err := rows.Scan(&item.msg.ID, &item.msg.Topic, &item.msg.AggregateID, &item.msg.CampaignID,
			&item.msg.Attempts, &item.msg.MaxAttempts, &item.status); err != nil {
			rows.Close()
			return err
		}
		expired = append(expired, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range expired {
		state := StateQueued
		if item.status == StatusFailed {
			state = StateFailed
		}
		if hook, ok := r.Hooks[item.msg.Topic]; ok {
			if err := hook(ctx, tx, item.msg, state, "worker lease expired"); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	metrics.Reclaimed.Add(int64(len(expired)))
	return nil
}

// cleanStream acknowledges entries that sat unacknowledged longer than
// RepublishAfter (their rows were re-queued above) and removes idle consumers
// left by old processes, so the pending-entries list does not grow forever.
func (r *Reaper) cleanStream(ctx context.Context) {
	start := "0-0"
	for {
		ids, next, err := r.Redis.XAutoClaimJustID(ctx, &redis.XAutoClaimArgs{
			Stream: r.Stream, Group: r.Group, Consumer: "reaper",
			MinIdle: r.RepublishAfter, Start: start, Count: 100,
		}).Result()
		if err != nil {
			return
		}
		if len(ids) > 0 {
			_ = r.Redis.XAck(ctx, r.Stream, r.Group, ids...).Err()
		}
		if next == "0-0" || len(ids) == 0 {
			break
		}
		start = next
	}
	consumers, err := r.Redis.XInfoConsumers(ctx, r.Stream, r.Group).Result()
	if err != nil {
		return
	}
	for _, consumer := range consumers {
		if consumer.Pending == 0 && consumer.Idle > time.Hour {
			_ = r.Redis.XGroupDelConsumer(ctx, r.Stream, r.Group, consumer.Name).Err()
		}
	}
}

func (r *Reaper) recordDepth(ctx context.Context) error {
	rows, err := r.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM outbox_messages
		WHERE status IN ('pending', 'published', 'sending') GROUP BY status`)
	if err != nil {
		return err
	}
	defer rows.Close()
	depth := map[string]int64{StatusPending: 0, StatusPublished: 0, StatusSending: 0}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return err
		}
		depth[status] = count
	}
	metrics.setDepth(depth)
	return rows.Err()
}
