ALTER TABLE conversations DROP COLUMN IF EXISTS waiting_closed_since;
DROP TABLE IF EXISTS message_idempotency_keys;
