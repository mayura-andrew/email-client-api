DROP TRIGGER IF EXISTS outbox_messages_touch ON outbox_messages;
DROP FUNCTION IF EXISTS outbox_messages_touch();
DROP INDEX IF EXISTS outbox_messages_updated_idx;
ALTER TABLE outbox_messages DROP COLUMN IF EXISTS updated_at;
