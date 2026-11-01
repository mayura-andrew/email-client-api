package data

import (
	"context"
	"database/sql"
	"time"
)

// DeliveryStats is the operator view of the outbox: what is waiting, what is
// moving, and how fast.
type DeliveryStats struct {
	Queue      QueueDepth        `json:"queue"`
	Today      DailyDelivery     `json:"today"`
	Throughput []ThroughputPoint `json:"throughput"`
	Recent     []DeliveryEvent   `json:"recent"`
	NextRetry  *time.Time        `json:"nextRetry"`
}

type QueueDepth struct {
	// Ready is pending and due now; Scheduled is waiting for a retry or the next quota day.
	Ready     int `json:"ready"`
	Scheduled int `json:"scheduled"`
	Published int `json:"published"`
	Sending   int `json:"sending"`
}

type DailyDelivery struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
}

type ThroughputPoint struct {
	Minute time.Time `json:"minute"`
	Sent   int       `json:"sent"`
	Failed int       `json:"failed"`
}

type DeliveryEvent struct {
	ID           int64     `json:"id"`
	Topic        string    `json:"topic"`
	Status       string    `json:"status"`
	Recipient    string    `json:"recipient"`
	Name         string    `json:"name"`
	CampaignID   *int64    `json:"campaignId"`
	CampaignName string    `json:"campaignName"`
	Attempts     int       `json:"attempts"`
	Error        string    `json:"error,omitempty"`
	At           time.Time `json:"at"`
}

type OutboxModel struct{ DB *sql.DB }

func (m OutboxModel) Stats(ctx context.Context, window time.Duration) (DeliveryStats, error) {
	stats := DeliveryStats{Throughput: []ThroughputPoint{}, Recent: []DeliveryEvent{}}

	err := m.DB.QueryRowContext(ctx, `SELECT
			COUNT(*) FILTER (WHERE status = 'pending' AND next_attempt_at <= NOW()),
			COUNT(*) FILTER (WHERE status = 'pending' AND next_attempt_at > NOW()),
			COUNT(*) FILTER (WHERE status = 'published'),
			COUNT(*) FILTER (WHERE status = 'sending'),
			MIN(next_attempt_at) FILTER (WHERE status = 'pending' AND next_attempt_at > NOW())
		FROM outbox_messages WHERE status IN ('pending', 'published', 'sending')`).
		Scan(&stats.Queue.Ready, &stats.Queue.Scheduled, &stats.Queue.Published, &stats.Queue.Sending, &stats.NextRetry)
	if err != nil {
		return stats, err
	}

	// "Today" is the UTC day, matching the SMTP daily quota window.
	err = m.DB.QueryRowContext(ctx, `SELECT
			COUNT(*) FILTER (WHERE status = 'sent'),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM outbox_messages
		WHERE status IN ('sent', 'failed') AND updated_at >= date_trunc('day', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`).Scan(&stats.Today.Sent, &stats.Today.Failed)
	if err != nil {
		return stats, err
	}

	// Aggregate the recent rows first (index range scan on updated_at), then
	// fill empty minutes so the chart has a continuous axis.
	rows, err := m.DB.QueryContext(ctx, `WITH bounds AS (
			SELECT date_trunc('minute', NOW()) - make_interval(mins => $1 - 1) AS start
		), buckets AS (
			SELECT date_trunc('minute', updated_at) AS minute,
				COUNT(*) FILTER (WHERE status = 'sent') AS sent,
				COUNT(*) FILTER (WHERE status = 'failed') AS failed
			FROM outbox_messages, bounds
			WHERE updated_at >= bounds.start AND status IN ('sent', 'failed')
			GROUP BY 1
		)
		SELECT series.minute, COALESCE(b.sent, 0), COALESCE(b.failed, 0)
		FROM bounds, generate_series(bounds.start, date_trunc('minute', NOW()), INTERVAL '1 minute') AS series(minute)
		LEFT JOIN buckets b ON b.minute = series.minute
		ORDER BY series.minute`, int(window.Minutes()))
	if err != nil {
		return stats, err
	}
	for rows.Next() {
		var point ThroughputPoint
		if err := rows.Scan(&point.Minute, &point.Sent, &point.Failed); err != nil {
			rows.Close()
			return stats, err
		}
		stats.Throughput = append(stats.Throughput, point)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return stats, err
	}

	stats.Recent, err = m.Recent(ctx, 12)
	return stats, err
}

// Recent lists the latest delivery outcomes, newest first, across both topics.
func (m OutboxModel) Recent(ctx context.Context, limit int) ([]DeliveryEvent, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT o.id, o.topic, o.status,
			COALESCE(cr.email, r.recipient, ''), COALESCE(cr.student_name, ''),
			o.campaign_id, COALESCE(c.name, ''), o.attempts, COALESCE(o.last_error, ''),
			o.updated_at
		FROM outbox_messages o
		LEFT JOIN campaign_recipients cr ON o.topic = 'certificate_email' AND cr.id = o.aggregate_id
		LEFT JOIN recipients r ON o.topic = 'tracked_email' AND r.id = o.aggregate_id
		LEFT JOIN campaigns c ON c.id = o.campaign_id
		WHERE o.status IN ('sent', 'failed', 'sending') OR (o.status = 'pending' AND o.attempts > 0)
		ORDER BY o.updated_at DESC, o.id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []DeliveryEvent{}
	for rows.Next() {
		var e DeliveryEvent
		var campaignID sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Topic, &e.Status, &e.Recipient, &e.Name, &campaignID, &e.CampaignName,
			&e.Attempts, &e.Error, &e.At); err != nil {
			return nil, err
		}
		if campaignID.Valid {
			e.CampaignID = &campaignID.Int64
		}
		if e.Status == "pending" {
			e.Status = "retrying"
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
