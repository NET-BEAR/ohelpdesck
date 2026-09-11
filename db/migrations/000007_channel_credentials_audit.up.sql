ALTER TABLE channels ADD COLUMN credentials_key_id text;
ALTER TABLE channels ADD COLUMN credentials_version smallint;
CREATE TABLE channel_audit_events (
  id uuid PRIMARY KEY, channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  actor_id uuid NOT NULL REFERENCES users(id), correlation_id uuid NOT NULL,
  action text NOT NULL, details jsonb NOT NULL DEFAULT '{}'::jsonb, occurred_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT channel_audit_action_not_blank CHECK (length(btrim(action)) > 0)
);
CREATE INDEX channel_audit_events_channel_idx ON channel_audit_events(channel_id, occurred_at DESC);
