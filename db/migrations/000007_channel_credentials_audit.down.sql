DROP TABLE IF EXISTS channel_audit_events;
ALTER TABLE channels DROP COLUMN IF EXISTS credentials_version;
ALTER TABLE channels DROP COLUMN IF EXISTS credentials_key_id;
