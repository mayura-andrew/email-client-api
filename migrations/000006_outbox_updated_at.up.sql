-- When a row last changed state, for the delivery activity feed and per-minute
-- throughput. A trigger keeps it correct for every writer (relay, worker, reaper).
ALTER TABLE outbox_messages ADD COLUMN updated_at TIMESTAMP(3) WITH TIME ZONE NOT NULL DEFAULT NOW();
UPDATE outbox_messages SET updated_at = COALESCE(sent_at, published_at, created_at);

CREATE FUNCTION outbox_messages_touch() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER outbox_messages_touch BEFORE UPDATE ON outbox_messages
    FOR EACH ROW EXECUTE FUNCTION outbox_messages_touch();

CREATE INDEX outbox_messages_updated_idx ON outbox_messages (updated_at DESC);
