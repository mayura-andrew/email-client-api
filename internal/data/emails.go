package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/mayura-andrew/email-client/internal/outbox"
	"github.com/mayura-andrew/email-client/internal/validator"
)

type Email struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"-"`
	Sender     string    `json:"sender"`
	Recipients []string  `json:"recipients,omitempty"`
	Body       string    `json:"body"`
	Subject    string    `json:"Subject"`
}

type EmailRecipient struct {
	ID         int64          `json:"id"`
	CreatedAt  time.Time      `json:"createdAt"`
	Sender     string         `json:"sender"`
	Recipient  string         `json:"recipients"`
	Body       string         `json:"body"`
	Subject    string         `json:"subject"`
	Status     bool           `json:"status"`
	SentTime   time.Time      `json:"sentTime"`
	Opened     bool           `json:"opened"`
	OpenedTime CustomNullTime `json:"openedTime"`
}

// TrackedDelivery is everything the worker needs to send one tracked email.
type TrackedDelivery struct {
	RecipientID int64
	Recipient   string
	Sender      string
	Subject     string
	Body        string
}

type EmailModel struct {
	DB *sql.DB
}

func ValidateEmail(v *validator.Validator, email *Email) {
	v.Check(email.Sender != "", "sender", "must be provided")
	v.Check(len(email.Recipients) != 0, "recipients", "must be provided")
	v.Check(len(email.Recipients) <= 10000, "recipients", "must not contain more than 10000 addresses")
	for _, recipient := range email.Recipients {
		if validator.EmailProblem(recipient) != "" {
			v.AddError("recipients", "must only contain valid email addresses")
			break
		}
	}
	v.Check(validator.Unique(email.Recipients), "recipients", "must not contain duplicate recipient emails")
	v.Check(email.Subject != "", "subject", "must be provided")
	v.Check(email.Body != "", "body", "must be provided")
}

// Queue stores the email, one row per recipient, and their outbox messages in a
// single transaction, then returns immediately; delivery happens in the workers.
func (e EmailModel) Queue(ctx context.Context, email *Email, maxAttempts int) ([]int64, error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, `INSERT INTO emails (sender, body, subject) VALUES ($1, $2, $3) RETURNING id, created_at`,
		email.Sender, email.Body, email.Subject).Scan(&email.ID, &email.CreatedAt); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `INSERT INTO recipients (email_id, recipient)
		SELECT $1, unnest($2::text[]) RETURNING id`, email.ID, pq.Array(email.Recipients))
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := outbox.EnqueueTx(ctx, tx, outbox.TopicTrackedEmail, sql.NullInt64{}, ids, maxAttempts); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

func (e EmailModel) List(ctx context.Context, recipient string, page Page) ([]EmailRecipient, int, error) {
	var total int
	if err := e.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM recipients WHERE ($1 = '' OR recipient = $1)`, recipient).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := e.DB.QueryContext(ctx, `SELECT r.id, r.recipient, r.status, r.sent_time, r.opened, r.opened_time,
			e.created_at, e.sender, e.body, e.subject
		FROM recipients r JOIN emails e ON r.email_id = e.id
		WHERE ($1 = '' OR r.recipient = $1)
		ORDER BY r.id DESC LIMIT $2 OFFSET $3`, recipient, page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	details := []EmailRecipient{}
	for rows.Next() {
		var d EmailRecipient
		if err := rows.Scan(&d.ID, &d.Recipient, &d.Status, &d.SentTime, &d.Opened, &d.OpenedTime,
			&d.CreatedAt, &d.Sender, &d.Body, &d.Subject); err != nil {
			return nil, 0, err
		}
		details = append(details, d)
	}
	return details, total, rows.Err()
}

func (e EmailModel) MarkOpened(ctx context.Context, id int64) error {
	result, err := e.DB.ExecContext(ctx, `UPDATE recipients SET opened = true, opened_time = COALESCE(opened_time, NOW()) WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrRecordNotFound
	}
	return nil
}

func (e EmailModel) TrackedDelivery(ctx context.Context, recipientID int64) (TrackedDelivery, error) {
	var d TrackedDelivery
	err := e.DB.QueryRowContext(ctx, `SELECT r.id, r.recipient, e.sender, e.subject, e.body
		FROM recipients r JOIN emails e ON e.id = r.email_id WHERE r.id = $1`, recipientID).
		Scan(&d.RecipientID, &d.Recipient, &d.Sender, &d.Subject, &d.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrRecordNotFound
	}
	return d, err
}

// TrackedHook records delivery on the recipients row inside the outbox transaction.
func TrackedHook(ctx context.Context, tx *sql.Tx, msg outbox.Message, state outbox.State, _ string) error {
	if state != outbox.StateSent {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE recipients SET status = true, sent_time = NOW() WHERE id = $1`, msg.AggregateID)
	return err
}
