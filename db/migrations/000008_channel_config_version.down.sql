ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_config_version_valid;
ALTER TABLE channels DROP COLUMN IF EXISTS config_version;
