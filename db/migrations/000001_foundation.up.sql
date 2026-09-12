CREATE TABLE IF NOT EXISTS schema_migrations (
  version bigint PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now(),
  checksum text
);
