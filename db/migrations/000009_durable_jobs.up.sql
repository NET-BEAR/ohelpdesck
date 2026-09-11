CREATE TYPE job_status AS ENUM ('pending','running','completed','dead','cancelled');

CREATE TABLE jobs (
  id uuid PRIMARY KEY,
  type text NOT NULL,
  handler text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  status job_status NOT NULL DEFAULT 'pending',
  priority integer NOT NULL DEFAULT 100,
  run_at timestamptz NOT NULL DEFAULT now(),
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 10,
  dedup_key text,
  event_id uuid REFERENCES outbox_events(id) ON DELETE CASCADE,
  locked_by text,
  locked_at timestamptz,
  lease_expires_at timestamptz,
  lease_token uuid,
  last_error_code text,
  last_error_message text,
  correlation_id uuid,
  causation_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  CONSTRAINT jobs_type_not_blank CHECK (length(btrim(type)) > 0),
  CONSTRAINT jobs_handler_not_blank CHECK (length(btrim(handler)) > 0),
  CONSTRAINT jobs_attempts_valid CHECK (attempts >= 0 AND max_attempts > 0 AND attempts <= max_attempts),
  CONSTRAINT jobs_running_lease_consistency CHECK (
    (status = 'running' AND locked_by IS NOT NULL AND locked_at IS NOT NULL AND lease_expires_at IS NOT NULL AND lease_token IS NOT NULL)
    OR (status <> 'running' AND locked_by IS NULL AND locked_at IS NULL AND lease_expires_at IS NULL AND lease_token IS NULL)
  )
);
CREATE INDEX jobs_claim_idx ON jobs(priority,run_at,created_at) WHERE status='pending';
CREATE INDEX jobs_running_lease_idx ON jobs(lease_expires_at) WHERE status='running';
CREATE INDEX jobs_dead_idx ON jobs(created_at) WHERE status='dead';
CREATE UNIQUE INDEX jobs_event_handler_uq ON jobs(event_id,handler) WHERE event_id IS NOT NULL;
CREATE UNIQUE INDEX jobs_type_dedup_active_uq ON jobs(type,dedup_key) WHERE dedup_key IS NOT NULL AND status IN ('pending','running');

CREATE TABLE event_handler_receipts (
  event_id uuid NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
  handler text NOT NULL,
  processed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id,handler),
  CONSTRAINT event_handler_receipts_handler_not_blank CHECK (length(btrim(handler)) > 0)
);
