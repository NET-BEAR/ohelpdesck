ALTER TABLE channels ADD COLUMN config_version bigint NOT NULL DEFAULT 1;
ALTER TABLE channels ADD CONSTRAINT channels_config_version_valid CHECK (config_version >= 1);
