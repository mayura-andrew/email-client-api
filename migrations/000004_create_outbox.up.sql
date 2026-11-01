ALTER TABLE campaigns ADD COLUMN batch_size INTEGER NOT NULL DEFAULT 50;

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

CREATE INDEX campaign_outbox_ready_idx
    ON campaign_outbox(status, next_attempt_at, id);
CREATE INDEX campaign_outbox_campaign_idx
    ON campaign_outbox(campaign_id, status, id);
