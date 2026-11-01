package outbox

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type Logger interface {
	PrintInfo(message string, properties map[string]string)
	PrintError(err error, properties map[string]string)
}

// Relay moves committed outbox rows into the Redis stream. Several replicas can
// run a relay concurrently: rows are claimed with FOR UPDATE SKIP LOCKED.
type Relay struct {
	DB     *sql.DB
	Redis  *redis.Client
	Stream string
	Logger Logger
	// BatchSize is the maximum number of rows published per round trip.
	BatchSize int
	// MaxInFlight caps published+sending rows so the stream stays short and
	// the stale-publish reaper never fires on messages that are merely queued.
	// It is a soft cap across replicas.
	MaxInFlight int
	// Interval is the fallback poll when no wake-up notification arrives.
	Interval time.Duration
}

func Notify(ctx context.Context, rdb *redis.Client) {
	_ = rdb.Publish(ctx, WakeChannel, "1").Err()
}

func (r *Relay) Run(ctx context.Context) {
	sub := r.Redis.Subscribe(ctx, WakeChannel)
	defer sub.Close()
	wake := sub.Channel()
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()

	for {
		for {
			published, err := r.PublishOnce(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r.Logger.PrintError(err, map[string]string{"component": "outbox-relay"})
				}
				break
			}
			if published < r.BatchSize {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

// PublishOnce publishes up to one batch and returns how many rows it published.
func (r *Relay) PublishOnce(ctx context.Context) (int, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var inFlight int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_messages WHERE status IN ('published', 'sending')`).Scan(&inFlight); err != nil {
		return 0, err
	}
	limit := r.MaxInFlight - inFlight
	if limit > r.BatchSize {
		limit = r.BatchSize
	}
	if limit <= 0 {
		return 0, nil
	}

	// Round-robin across campaigns (one-off emails share bucket 0) so a large
	// campaign cannot starve a small one that was started later.
	rows, err := tx.QueryContext(ctx, `WITH ranked AS (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY COALESCE(campaign_id, 0) ORDER BY next_attempt_at, id) AS rn
			FROM outbox_messages
			WHERE status = 'pending' AND next_attempt_at <= NOW()
		)
		SELECT o.id, o.topic
		FROM outbox_messages o JOIN ranked r ON r.id = o.id
		WHERE o.status = 'pending' AND o.next_attempt_at <= NOW()
		ORDER BY r.rn, o.id
		LIMIT $1
		FOR UPDATE OF o SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	var ids []int64
	var topics []string
	for rows.Next() {
		var id int64
		var topic string
		if err := rows.Scan(&id, &topic); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		topics = append(topics, topic)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	// The rows are committed as published BEFORE the stream entries exist. In
	// the other order a fast worker can read an entry while its row still looks
	// pending, treat it as a duplicate and acknowledge it, stranding the row.
	if _, err := tx.ExecContext(ctx, `UPDATE outbox_messages SET status = 'published', published_at = NOW()
		WHERE id = ANY($1)`, pq.Array(ids)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	pipe := r.Redis.Pipeline()
	for i, id := range ids {
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: r.Stream,
			MaxLen: 100000,
			Approx: true,
			Values: map[string]any{"id": strconv.FormatInt(id, 10), "topic": topics[i]},
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		// Put the rows back so they are published on the next round instead of
		// waiting for the reaper. Entries that did reach the stream will find
		// their row pending and be skipped. A crash right here is covered by the
		// reaper's RepublishAfter.
		_, revertErr := r.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE outbox_messages SET status = 'pending', published_at = NULL
			WHERE id = ANY($1) AND status = 'published'`, pq.Array(ids))
		return 0, errors.Join(err, revertErr)
	}
	metrics.Published.Add(int64(len(ids)))
	return len(ids), nil
}
