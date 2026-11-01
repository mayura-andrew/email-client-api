package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/mayura-andrew/email-client/internal/outbox"
)

var ErrCampaignNotDraft = errors.New("campaign is no longer a draft")

type Campaign struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Subject     string     `json:"subject"`
	Body        string     `json:"body"`
	Sender      string     `json:"sender"`
	Status      string     `json:"status"`
	TotalCount  int        `json:"totalCount"`
	ValidCount  int        `json:"validCount"`
	SentCount   int        `json:"sentCount"`
	FailedCount int        `json:"failedCount"`
	BatchSize   int        `json:"batchSize"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	// Breakdown counts recipients per status; only filled by Get.
	Breakdown map[string]int `json:"breakdown,omitempty"`
}

// RecipientFilter narrows a recipient listing; empty fields match everything.
type RecipientFilter struct {
	Status string
	Search string
}

type CampaignRecipient struct {
	ID           int64  `json:"id"`
	CampaignID   int64  `json:"campaignId"`
	StudentName  string `json:"studentName"`
	Email        string `json:"email"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// CertificateDelivery is everything the worker needs to send one certificate.
type CertificateDelivery struct {
	Recipient CampaignRecipient
	Subject   string
	Body      string
	Sender    string
}

type ImportResult struct {
	Inserted int
}

type CampaignModel struct{ DB *sql.DB }

// Sent and failed counts are derived from campaign_recipients rather than
// incremented on the campaign row by every worker.
const campaignColumns = `c.id, c.name, c.subject, c.body, c.sender, c.status, c.total_count, c.valid_count,
	COUNT(r.id) FILTER (WHERE r.status = 'sent'), COUNT(r.id) FILTER (WHERE r.status = 'failed'),
	c.batch_size, c.created_at, c.started_at, c.completed_at`

func scanCampaign(scanner interface{ Scan(...any) error }, c *Campaign) error {
	return scanner.Scan(&c.ID, &c.Name, &c.Subject, &c.Body, &c.Sender, &c.Status, &c.TotalCount, &c.ValidCount,
		&c.SentCount, &c.FailedCount, &c.BatchSize, &c.CreatedAt, &c.StartedAt, &c.CompletedAt)
}

func (m CampaignModel) Create(ctx context.Context, c *Campaign) error {
	if c.BatchSize < 1 {
		c.BatchSize = 50
	}
	c.Status = "draft"
	return m.DB.QueryRowContext(ctx, `INSERT INTO campaigns (name, subject, body, sender, batch_size) VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		c.Name, c.Subject, c.Body, c.Sender, c.BatchSize).Scan(&c.ID, &c.CreatedAt)
}

func (m CampaignModel) Get(ctx context.Context, id int64) (Campaign, error) {
	var c Campaign
	err := scanCampaign(m.DB.QueryRowContext(ctx, `SELECT `+campaignColumns+`
		FROM campaigns c LEFT JOIN campaign_recipients r ON r.campaign_id = c.id
		WHERE c.id = $1 GROUP BY c.id`, id), &c)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrRecordNotFound
	}
	if err != nil {
		return c, err
	}
	rows, err := m.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM campaign_recipients WHERE campaign_id = $1 GROUP BY status`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	c.Breakdown = map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return c, err
		}
		c.Breakdown[status] = count
	}
	return c, rows.Err()
}

func (m CampaignModel) List(ctx context.Context, page Page) ([]Campaign, int, error) {
	var total int
	if err := m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM campaigns`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.DB.QueryContext(ctx, `SELECT `+campaignColumns+`
		FROM campaigns c LEFT JOIN campaign_recipients r ON r.campaign_id = c.id
		GROUP BY c.id ORDER BY c.created_at DESC, c.id DESC LIMIT $1 OFFSET $2`, page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	campaigns := []Campaign{}
	for rows.Next() {
		var c Campaign
		if err := scanCampaign(rows, &c); err != nil {
			return nil, 0, err
		}
		campaigns = append(campaigns, c)
	}
	return campaigns, total, rows.Err()
}

// ImportRecipients inserts a validated CSV in one statement and one transaction:
// either every row lands or none do. Emails already in the campaign are skipped.
func (m CampaignModel) ImportRecipients(ctx context.Context, campaignID int64, names, emails []string, totalRows int) (ImportResult, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()

	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, campaignID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportResult{}, ErrRecordNotFound
	}
	if err != nil {
		return ImportResult{}, err
	}
	if status != "draft" {
		return ImportResult{}, ErrCampaignNotDraft
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO campaign_recipients (campaign_id, student_name, email, status)
		SELECT $1, name, email, 'valid' FROM unnest($2::text[], $3::text[]) AS t(name, email)
		ON CONFLICT (campaign_id, email) DO NOTHING`, campaignID, pq.Array(names), pq.Array(emails))
	if err != nil {
		return ImportResult{}, err
	}
	inserted, _ := result.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE campaigns SET total_count = total_count + $2,
		valid_count = (SELECT COUNT(*) FROM campaign_recipients WHERE campaign_id = $1)
		WHERE id = $1`, campaignID, totalRows); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Inserted: int(inserted)}, tx.Commit()
}

// Start moves the campaign to running and enqueues one outbox message per
// recipient in the same transaction. Previously failed messages are reset so
// the retry flow (RetryFailed + Start) reuses their outbox rows.
func (m CampaignModel) Start(ctx context.Context, id int64, maxAttempts int) (int, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE campaigns SET status = 'running', started_at = COALESCE(started_at, NOW()), completed_at = NULL
		WHERE id = $1 AND status IN ('draft', 'paused')`, id)
	if err != nil {
		return 0, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return 0, ErrRecordNotFound
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO outbox_messages (topic, aggregate_id, campaign_id, max_attempts)
		SELECT $2, id, campaign_id, $3 FROM campaign_recipients
		WHERE campaign_id = $1 AND status = 'valid'
		ON CONFLICT (topic, aggregate_id) DO UPDATE SET status = 'pending', attempts = 0, next_attempt_at = NOW(),
			published_at = NULL, locked_until = NULL, locked_by = NULL, last_error = NULL, max_attempts = EXCLUDED.max_attempts
		WHERE outbox_messages.status = 'failed'`, id, outbox.TopicCertificateEmail, maxAttempts)
	if err != nil {
		return 0, err
	}
	queued, _ := result.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'queued'
		WHERE campaign_id = $1 AND status = 'valid'`, id); err != nil {
		return 0, err
	}
	return int(queued), tx.Commit()
}

func (m CampaignModel) RetryFailed(ctx context.Context, id int64) (int, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'valid', error_message = NULL
		WHERE campaign_id = $1 AND status = 'failed'`, id)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	if count > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE campaigns SET status = 'paused'
			WHERE id = $1 AND status IN ('running', 'completed')`, id); err != nil {
			return 0, err
		}
	}
	return int(count), tx.Commit()
}

// CompleteFinished marks running campaigns completed once nothing is left in flight.
func (m CampaignModel) CompleteFinished(ctx context.Context) error {
	_, err := m.DB.ExecContext(ctx, `UPDATE campaigns c SET status = 'completed', completed_at = NOW()
		WHERE c.status = 'running' AND NOT EXISTS (
			SELECT 1 FROM outbox_messages o
			WHERE o.campaign_id = c.id AND o.status IN ('pending', 'published', 'sending'))`)
	return err
}

func (m CampaignModel) Recipients(ctx context.Context, id int64, filter RecipientFilter, page Page) ([]CampaignRecipient, int, error) {
	const where = `WHERE campaign_id = $1 AND ($2 = '' OR status = $2)
		AND ($3 = '' OR student_name ILIKE '%' || $3 || '%' OR email ILIKE '%' || $3 || '%')`
	var total int
	if err := m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM campaign_recipients `+where, id, filter.Status, filter.Search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := m.DB.QueryContext(ctx, `SELECT id, campaign_id, student_name, email, status, COALESCE(error_message, '')
		FROM campaign_recipients `+where+` ORDER BY id LIMIT $4 OFFSET $5`, id, filter.Status, filter.Search, page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	recipients := []CampaignRecipient{}
	for rows.Next() {
		var r CampaignRecipient
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.StudentName, &r.Email, &r.Status, &r.ErrorMessage); err != nil {
			return nil, 0, err
		}
		recipients = append(recipients, r)
	}
	return recipients, total, rows.Err()
}

func (m CampaignModel) CertificateDelivery(ctx context.Context, recipientID int64) (CertificateDelivery, error) {
	var d CertificateDelivery
	err := m.DB.QueryRowContext(ctx, `SELECT r.id, r.campaign_id, r.student_name, r.email, r.status, c.subject, c.body, c.sender
		FROM campaign_recipients r JOIN campaigns c ON c.id = r.campaign_id WHERE r.id = $1`, recipientID).
		Scan(&d.Recipient.ID, &d.Recipient.CampaignID, &d.Recipient.StudentName, &d.Recipient.Email, &d.Recipient.Status,
			&d.Subject, &d.Body, &d.Sender)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrRecordNotFound
	}
	return d, err
}

// CertificateHook keeps campaign_recipients in step with the outbox, inside the
// outbox transaction, so the UI never disagrees with the delivery state.
func CertificateHook(ctx context.Context, tx *sql.Tx, msg outbox.Message, state outbox.State, errText string) error {
	var err error
	switch state {
	case outbox.StateSent:
		_, err = tx.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'sent', sent_at = NOW(), error_message = NULL WHERE id = $1`, msg.AggregateID)
	case outbox.StateFailed:
		_, err = tx.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'failed', error_message = $2 WHERE id = $1`, msg.AggregateID, errText)
	default:
		_, err = tx.ExecContext(ctx, `UPDATE campaign_recipients SET status = $2, error_message = NULLIF($3, '') WHERE id = $1`,
			msg.AggregateID, string(state), errText)
	}
	return err
}
