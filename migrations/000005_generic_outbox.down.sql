DROP TABLE IF EXISTS certificate_templates;

DROP INDEX IF EXISTS recipients_recipient_idx;
DROP INDEX IF EXISTS recipients_email_id_idx;
ALTER TABLE recipients ALTER COLUMN email_id TYPE INTEGER;

DROP INDEX IF EXISTS campaign_recipients_campaign_status_idx;

ALTER TABLE campaigns ADD COLUMN sent_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE campaigns ADD COLUMN failed_count INTEGER NOT NULL DEFAULT 0;
UPDATE campaigns c SET
    sent_count = (SELECT COUNT(*) FROM campaign_recipients r WHERE r.campaign_id = c.id AND r.status = 'sent'),
    failed_count = (SELECT COUNT(*) FROM campaign_recipients r WHERE r.campaign_id = c.id AND r.status = 'failed');

CREATE TABLE campaign_outbox (
    id BIGSERIAL PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES campaign_recipients(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT NOW(),
    locked_at TIMESTAMP(0) WITH TIME ZONE,
    last_error TEXT,
    sent_at TIMESTAMP(0) WITH TIME ZONE,
    created_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(recipient_id)
);
CREATE INDEX campaign_outbox_ready_idx ON campaign_outbox(status, next_attempt_at, id);
CREATE INDEX campaign_outbox_campaign_idx ON campaign_outbox(campaign_id, status, id);

INSERT INTO campaign_outbox (campaign_id, recipient_id, status, attempts, next_attempt_at, last_error, sent_at, created_at)
SELECT campaign_id, aggregate_id,
       CASE status WHEN 'sent' THEN 'sent' WHEN 'failed' THEN 'failed' ELSE 'pending' END,
       attempts, next_attempt_at, last_error, sent_at, created_at
FROM outbox_messages
WHERE topic = 'certificate_email' AND campaign_id IS NOT NULL;

DROP TABLE outbox_messages;
