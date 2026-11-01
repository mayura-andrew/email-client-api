package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Processor delivers one message. Each worker goroutine owns its own Processor,
// so implementations can hold a long-lived connection (e.g. SMTP) without locking.
type Processor interface {
	Process(ctx context.Context, msg Message) error
	Close() error
}

type Worker struct {
	DB           *sql.DB
	Redis        *redis.Client
	Stream       string
	Group        string
	ConsumerName string
	Concurrency  int
	// Lease is how long a claimed message is owned by one consumer; processing
	// is cancelled when it runs out so the reaper never races a live send.
	Lease        time.Duration
	NewProcessor func() Processor
	Hooks        map[string]StateHook
	Logger       Logger
}

func deliveredKey(id int64) string { return "outbox:delivered:" + strconv.FormatInt(id, 10) }

func EnsureGroup(ctx context.Context, rdb *redis.Client, stream, group string) error {
	err := rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Run blocks until ctx is cancelled and every in-flight message has finished.
func (w *Worker) Run(ctx context.Context) error {
	if err := EnsureGroup(ctx, w.Redis, w.Stream, w.Group); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func(consumer string) {
			defer wg.Done()
			w.consume(ctx, consumer)
		}(fmt.Sprintf("%s-%d", w.ConsumerName, i))
	}
	wg.Wait()
	return nil
}

func (w *Worker) consume(ctx context.Context, consumer string) {
	processor := w.NewProcessor()
	defer processor.Close()
	// Redis calls use a non-cancellable context: cancelling a blocked
	// XREADGROUP can drop a reply after Redis already assigned the entry to this
	// consumer, stranding it. Shutdown is noticed between reads instead.
	redisCtx := context.WithoutCancel(ctx)
	nextRecovery := time.Now()
	for ctx.Err() == nil {
		var entries []redis.XMessage
		if time.Now().After(nextRecovery) {
			entries = w.reclaimAbandoned(redisCtx, consumer)
			nextRecovery = time.Now().Add(w.Lease / 2)
		}
		if len(entries) == 0 {
			streams, err := w.Redis.XReadGroup(redisCtx, &redis.XReadGroupArgs{
				Group:    w.Group,
				Consumer: consumer,
				Streams:  []string{w.Stream, ">"},
				Count:    1,
				Block:    2 * time.Second,
			}).Result()
			if err != nil {
				if !errors.Is(err, redis.Nil) {
					w.Logger.PrintError(err, map[string]string{"component": "outbox-worker", "consumer": consumer})
					sleep(ctx, time.Second)
				}
				continue
			}
			for _, stream := range streams {
				entries = append(entries, stream.Messages...)
			}
		}
		for _, entry := range entries {
			// In-flight work finishes even after shutdown starts, bounded by the
			// lease; otherwise the SMTP send would be cut mid-DATA.
			workCtx, cancel := context.WithTimeout(redisCtx, w.Lease)
			w.handle(workCtx, consumer, processor, entry)
			cancel()
			_ = w.Redis.XAck(redisCtx, w.Stream, w.Group, entry.ID).Err()
		}
	}
}

// reclaimAbandoned takes over entries that a crashed consumer received but
// never acknowledged. Anything idle longer than the lease cannot still be in
// progress, and the conditional claim makes a stolen-but-finished entry a no-op.
func (w *Worker) reclaimAbandoned(ctx context.Context, consumer string) []redis.XMessage {
	entries, _, err := w.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: w.Stream, Group: w.Group, Consumer: consumer,
		MinIdle: w.Lease + 10*time.Second, Start: "0-0", Count: 10,
	}).Result()
	if err != nil {
		return nil
	}
	return entries
}

func (w *Worker) handle(ctx context.Context, consumer string, processor Processor, entry redis.XMessage) {
	raw, _ := entry.Values["id"].(string)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		w.Logger.PrintError(fmt.Errorf("malformed outbox entry %s", entry.ID), nil)
		return
	}
	msg, ok, err := w.claim(ctx, id, consumer)
	if err != nil {
		// The row stays published; the reaper will put it back to pending.
		w.Logger.PrintError(err, map[string]string{"operation": "outbox_claim", "outbox_id": raw})
		return
	}
	if !ok {
		metrics.Duplicates.Add(1)
		return
	}

	// A previous attempt delivered the message but crashed before recording it.
	if delivered, _ := w.Redis.Exists(ctx, deliveredKey(id)).Result(); delivered == 1 {
		w.finish(ctx, msg, nil)
		return
	}

	start := time.Now()
	err = process(ctx, processor, msg)
	if err == nil {
		metrics.SendMillis.Add(time.Since(start).Milliseconds())
		_ = w.Redis.Set(ctx, deliveredKey(id), "1", 7*24*time.Hour).Err()
	}
	w.finish(ctx, msg, err)
}

func process(ctx context.Context, processor Processor, msg Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("processor panic: %v", r)
		}
	}()
	return processor.Process(ctx, msg)
}

func (w *Worker) claim(ctx context.Context, id int64, consumer string) (Message, bool, error) {
	tx, err := w.DB.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()
	var msg Message
	err = tx.QueryRowContext(ctx, `UPDATE outbox_messages
		SET status = 'sending', attempts = attempts + 1, locked_by = $2,
			locked_until = NOW() + make_interval(secs => $3)
		WHERE id = $1 AND status = 'published'
		RETURNING id, topic, aggregate_id, campaign_id, attempts, max_attempts`,
		id, consumer, w.Lease.Seconds()).
		Scan(&msg.ID, &msg.Topic, &msg.AggregateID, &msg.CampaignID, &msg.Attempts, &msg.MaxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	if err := w.hook(ctx, tx, msg, StateSending, ""); err != nil {
		return Message{}, false, err
	}
	return msg, true, tx.Commit()
}

func (w *Worker) finish(ctx context.Context, msg Message, sendErr error) {
	tx, err := w.DB.BeginTx(ctx, nil)
	if err != nil {
		w.Logger.PrintError(err, map[string]string{"operation": "outbox_finish", "outbox_id": strconv.FormatInt(msg.ID, 10)})
		return
	}
	defer tx.Rollback()

	var state State
	errText := ""
	if sendErr != nil {
		errText = truncate(sendErr.Error(), 1000)
	}
	switch until, deferred := deferredUntil(sendErr); {
	case sendErr == nil:
		state = StateSent
		_, err = tx.ExecContext(ctx, `UPDATE outbox_messages SET status = 'sent', sent_at = NOW(),
			locked_until = NULL, locked_by = NULL, last_error = NULL WHERE id = $1`, msg.ID)
	case deferred:
		state = StateQueued
		_, err = tx.ExecContext(ctx, `UPDATE outbox_messages SET status = 'pending', attempts = GREATEST(attempts - 1, 0),
			next_attempt_at = $2, locked_until = NULL, locked_by = NULL, last_error = $3 WHERE id = $1`, msg.ID, until, errText)
	case IsPermanent(sendErr) || msg.Attempts >= msg.MaxAttempts:
		state = StateFailed
		_, err = tx.ExecContext(ctx, `UPDATE outbox_messages SET status = 'failed',
			locked_until = NULL, locked_by = NULL, last_error = $2 WHERE id = $1`, msg.ID, errText)
	default:
		state = StateRetrying
		_, err = tx.ExecContext(ctx, `UPDATE outbox_messages SET status = 'pending', next_attempt_at = $2,
			locked_until = NULL, locked_by = NULL, last_error = $3 WHERE id = $1`, msg.ID, time.Now().Add(Backoff(msg.Attempts)), errText)
	}
	if err == nil {
		err = w.hook(ctx, tx, msg, state, errText)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		// The lease will expire and the reaper requeues; the delivered marker
		// prevents a resend if the send itself succeeded.
		w.Logger.PrintError(err, map[string]string{"operation": "outbox_finish", "outbox_id": strconv.FormatInt(msg.ID, 10)})
		return
	}

	properties := map[string]string{"outbox_id": strconv.FormatInt(msg.ID, 10), "topic": msg.Topic,
		"attempt": strconv.Itoa(msg.Attempts), "state": string(state)}
	switch state {
	case StateSent:
		metrics.Sent.Add(1)
		w.Logger.PrintInfo("outbox message delivered", properties)
	case StateQueued:
		metrics.Deferred.Add(1)
		w.Logger.PrintInfo("outbox message deferred", properties)
	case StateRetrying:
		metrics.Retried.Add(1)
		w.Logger.PrintError(sendErr, properties)
	case StateFailed:
		metrics.Failed.Add(1)
		w.Logger.PrintError(sendErr, properties)
	}
}

func (w *Worker) hook(ctx context.Context, tx *sql.Tx, msg Message, state State, errText string) error {
	if hook, ok := w.Hooks[msg.Topic]; ok {
		return hook(ctx, tx, msg, state, errText)
	}
	return nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
