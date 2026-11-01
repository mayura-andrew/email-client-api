// Package outbox implements the transactional outbox pattern on top of
// PostgreSQL (source of truth) and a Redis stream (delivery transport).
//
// Lifecycle of a row in outbox_messages:
//
//	pending ──relay──▶ published ──worker claim──▶ sending ──▶ sent
//	   ▲                   │                          │
//	   └──── reaper ◀──────┘ (stale)                  ├──▶ pending (retry with backoff)
//	   └──── reaper ◀───────────────── (lease expired)┘──▶ failed  (permanent / attempts exhausted)
//
// Delivery is at-least-once. The conditional claim (status='published') makes
// duplicate stream entries harmless, and a Redis "delivered" marker narrows the
// window in which a crash between the SMTP send and the DB commit causes a resend.
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"math/rand"
	"time"

	"github.com/lib/pq"
)

const (
	TopicCertificateEmail = "certificate_email"
	TopicTrackedEmail     = "tracked_email"

	StatusPending   = "pending"
	StatusPublished = "published"
	StatusSending   = "sending"
	StatusSent      = "sent"
	StatusFailed    = "failed"

	// WakeChannel is a Redis pub/sub channel that tells relays new rows were committed.
	WakeChannel = "outbox:wake"
)

type Message struct {
	ID          int64
	Topic       string
	AggregateID int64
	CampaignID  sql.NullInt64
	Attempts    int
	MaxAttempts int
}

// State is the outcome reported to a topic's StateHook so it can keep its own
// table (campaign_recipients, recipients) consistent inside the same transaction.
type State string

const (
	StateSending  State = "sending"
	StateSent     State = "sent"
	StateRetrying State = "retrying"
	StateFailed   State = "failed"
	StateQueued   State = "queued"
)

type StateHook func(ctx context.Context, tx *sql.Tx, msg Message, state State, errText string) error

// permanentError marks a failure that retrying will not fix (e.g. SMTP 550 unknown mailbox).
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// deferError reschedules a message without consuming an attempt (e.g. daily quota reached).
type deferError struct {
	err   error
	until time.Time
}

func (e deferError) Error() string { return e.err.Error() }
func (e deferError) Unwrap() error { return e.err }

func Defer(err error, until time.Time) error {
	return deferError{err: err, until: until}
}

func deferredUntil(err error) (time.Time, bool) {
	var d deferError
	if errors.As(err, &d) {
		return d.until, true
	}
	return time.Time{}, false
}

// Backoff returns the delay before retry number `attempt` (1-based):
// 30s, 1m, 2m, 4m ... capped at 30m, with ±20% jitter so retries don't stampede.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for i := 1; i < attempt && delay < 30*time.Minute; i++ {
		delay *= 2
	}
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(delay) * jitter)
}

// EnqueueTx inserts outbox rows inside the caller's transaction so the message
// exists if and only if the business change commits.
func EnqueueTx(ctx context.Context, tx *sql.Tx, topic string, campaignID sql.NullInt64, aggregateIDs []int64, maxAttempts int) error {
	if len(aggregateIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox_messages (topic, aggregate_id, campaign_id, max_attempts)
		SELECT $1, unnest($2::bigint[]), $3, $4
		ON CONFLICT (topic, aggregate_id) DO NOTHING`, topic, pq.Array(aggregateIDs), campaignID, maxAttempts)
	return err
}
