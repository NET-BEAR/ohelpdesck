CREATE TABLE message_idempotency_keys (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  idempotency_key text NOT NULL,
  request_hash text NOT NULL,
  message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, idempotency_key),
  CONSTRAINT message_idempotency_key_not_blank CHECK (length(btrim(idempotency_key)) > 0)
);

-- A definitive provider correction must restore the original start of the
-- waiting episode, not merely the latest inbound timestamp.
ALTER TABLE conversations ADD COLUMN waiting_closed_since timestamptz;
