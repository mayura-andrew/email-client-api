-- Generic transactional outbox shared by every message type (campaign certificates
-- and tracked one-off emails). Rows are written in the same transaction as the
-- business change, relayed to a Redis stream, and consumed by the worker pool.
CREATE TABLE outbox_messages (
    id BIGSERIAL PRIMARY KEY,
    topic VARCHAR(64) NOT NULL,
    aggregate_id BIGINT NOT NULL,
    campaign_id BIGINT REFERENCES campaigns(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'published', 'sending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    next_attempt_at TIMESTAMP(3) WITH TIME ZONE NOT NULL DEFAULT NOW(),
    published_at TIMESTAMP(3) WITH TIME ZONE,
    locked_until TIMESTAMP(3) WITH TIME ZONE,
    locked_by TEXT,
    last_error TEXT,
    sent_at TIMESTAMP(3) WITH TIME ZONE,
    created_at TIMESTAMP(3) WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (topic, aggregate_id)
);

CREATE INDEX outbox_messages_pending_idx
    ON outbox_messages (next_attempt_at, id) WHERE status = 'pending';
CREATE INDEX outbox_messages_inflight_idx
    ON outbox_messages (status, locked_until, published_at) WHERE status IN ('published', 'sending');
CREATE INDEX outbox_messages_campaign_idx
    ON outbox_messages (campaign_id, status);

INSERT INTO outbox_messages (topic, aggregate_id, campaign_id, status, attempts, next_attempt_at, last_error, sent_at, created_at)
SELECT 'certificate_email', recipient_id, campaign_id,
       CASE status WHEN 'sent' THEN 'sent' WHEN 'failed' THEN 'failed' ELSE 'pending' END,
       attempts, next_attempt_at, last_error, sent_at, created_at
FROM campaign_outbox;

DROP TABLE campaign_outbox;

-- Progress counters are derived from campaign_recipients instead of being
-- incremented on a single hot row by every worker.
ALTER TABLE campaigns DROP COLUMN sent_count;
ALTER TABLE campaigns DROP COLUMN failed_count;

CREATE INDEX campaign_recipients_campaign_status_idx
    ON campaign_recipients (campaign_id, status);

ALTER TABLE recipients ALTER COLUMN email_id TYPE BIGINT;
CREATE INDEX recipients_email_id_idx ON recipients (email_id);
CREATE INDEX recipients_recipient_idx ON recipients (recipient);

-- Shared by every replica, replacing the template file on the local volume.
CREATE TABLE certificate_templates (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    filename VARCHAR(255) NOT NULL,
    content BYTEA NOT NULL,
    updated_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT NOW()
);
