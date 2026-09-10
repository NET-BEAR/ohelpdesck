CREATE TYPE channel_type AS ENUM ('telegram', 'vk', 'max', 'email');
CREATE TYPE channel_status AS ENUM ('active', 'degraded', 'reauthorization_required', 'disabled', 'error');
CREATE TYPE conversation_status AS ENUM ('open', 'pending', 'resolved', 'snoozed');
CREATE TYPE conversation_priority AS ENUM ('low', 'normal', 'high', 'urgent');
CREATE TYPE message_direction AS ENUM ('incoming', 'outgoing', 'internal', 'system');
CREATE TYPE message_actor_type AS ENUM ('customer', 'agent', 'ai', 'workflow', 'system');
CREATE TYPE message_content_type AS ENUM ('text', 'html', 'mixed', 'attachment_only', 'unsupported');
CREATE TYPE message_status AS ENUM ('received', 'queued', 'sent', 'delivered', 'read', 'failed');

CREATE TABLE channels (
  id uuid PRIMARY KEY, type channel_type NOT NULL, name text NOT NULL,
  status channel_status NOT NULL DEFAULT 'disabled', external_account_id text,
  config jsonb NOT NULL DEFAULT '{}'::jsonb, credentials_ciphertext bytea, webhook_secret_ciphertext bytea,
  enabled boolean NOT NULL DEFAULT false, last_inbound_at timestamptz, last_outbound_at timestamptz,
  last_success_at timestamptz, last_error_at timestamptz, last_error_code text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT channels_name_not_blank CHECK (length(btrim(name)) > 0)
);
CREATE INDEX channels_type_idx ON channels(type);
CREATE INDEX channels_enabled_idx ON channels(enabled) WHERE enabled;

CREATE TABLE contacts (
  id uuid PRIMARY KEY, internal_customer_id text, name text NOT NULL DEFAULT '', email text, phone text,
  avatar_url text, attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX contacts_internal_customer_id_uq ON contacts(internal_customer_id) WHERE internal_customer_id IS NOT NULL;
CREATE INDEX contacts_email_lower_idx ON contacts(lower(email)) WHERE email IS NOT NULL;
CREATE INDEX contacts_phone_idx ON contacts(phone) WHERE phone IS NOT NULL;

CREATE TABLE contact_identities (
  id uuid PRIMARY KEY, contact_id uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE, external_user_id text NOT NULL,
  external_chat_id text, username text, display_name text, metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT contact_identities_external_user_nonempty CHECK (length(btrim(external_user_id)) > 0)
);
CREATE UNIQUE INDEX contact_identities_channel_user_uq ON contact_identities(channel_id, external_user_id);
CREATE INDEX contact_identities_contact_idx ON contact_identities(contact_id);

CREATE TABLE channel_memberships (
  channel_id uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  can_read boolean NOT NULL DEFAULT false, can_reply boolean NOT NULL DEFAULT false, can_reassign boolean NOT NULL DEFAULT false,
  PRIMARY KEY(channel_id, user_id)
);

CREATE SEQUENCE conversation_number_seq;
CREATE TABLE conversations (
  id uuid PRIMARY KEY, number bigint NOT NULL DEFAULT nextval('conversation_number_seq'),
  contact_id uuid NOT NULL REFERENCES contacts(id), contact_identity_id uuid NOT NULL REFERENCES contact_identities(id),
  channel_id uuid NOT NULL REFERENCES channels(id), external_thread_id text NOT NULL, assignee_id uuid REFERENCES users(id),
  status conversation_status NOT NULL DEFAULT 'open', priority conversation_priority NOT NULL DEFAULT 'normal', subject text,
  waiting_since timestamptz, first_response_at timestamptz, last_inbound_at timestamptz, last_outbound_at timestamptz,
  last_activity_at timestamptz NOT NULL, resolved_at timestamptz, snoozed_until timestamptz,
  waiting_closed_by_message_id uuid, metadata jsonb NOT NULL DEFAULT '{}'::jsonb, version bigint NOT NULL DEFAULT 1,
  is_current boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT conversations_version_valid CHECK (version >= 1),
  CONSTRAINT conversations_thread_not_blank CHECK (length(btrim(external_thread_id)) > 0),
  CONSTRAINT conversations_snooze_consistency CHECK ((status = 'snoozed' AND snoozed_until IS NOT NULL) OR status <> 'snoozed')
);
CREATE UNIQUE INDEX conversations_number_uq ON conversations(number);
CREATE UNIQUE INDEX conversations_current_thread_uq ON conversations(channel_id, external_thread_id, contact_identity_id) WHERE is_current;
CREATE INDEX conversations_thread_lookup_idx ON conversations(channel_id, external_thread_id, contact_identity_id, created_at DESC);
CREATE INDEX conversations_contact_idx ON conversations(contact_id);
CREATE INDEX conversations_identity_idx ON conversations(contact_identity_id);
CREATE INDEX conversations_channel_status_activity_idx ON conversations(channel_id, status, last_activity_at DESC);
CREATE INDEX conversations_assignee_status_idx ON conversations(assignee_id, status) WHERE assignee_id IS NOT NULL;
CREATE INDEX conversations_waiting_idx ON conversations(waiting_since) WHERE waiting_since IS NOT NULL;

CREATE TABLE messages (
  id uuid PRIMARY KEY, conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  channel_id uuid NOT NULL REFERENCES channels(id), external_message_id text, direction message_direction NOT NULL,
  actor_type message_actor_type NOT NULL, actor_user_id uuid REFERENCES users(id), content_type message_content_type NOT NULL DEFAULT 'text',
  text_content text, html_content text, reply_to_message_id uuid REFERENCES messages(id), status message_status NOT NULL,
  external_created_at timestamptz, received_at timestamptz, queued_at timestamptz, sent_at timestamptz, delivered_at timestamptz, read_at timestamptz,
  error_code text, error_message text, metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT messages_agent_actor_consistency CHECK ((actor_type = 'agent' AND actor_user_id IS NOT NULL) OR (actor_type <> 'agent' AND actor_user_id IS NULL)),
  CONSTRAINT messages_incoming_status_consistency CHECK (direction <> 'incoming' OR status = 'received')
);
ALTER TABLE conversations ADD CONSTRAINT conversations_waiting_closed_message_fk FOREIGN KEY(waiting_closed_by_message_id) REFERENCES messages(id);
CREATE UNIQUE INDEX messages_channel_external_id_uq ON messages(channel_id, external_message_id) WHERE external_message_id IS NOT NULL;
CREATE INDEX messages_conversation_created_idx ON messages(conversation_id, created_at, id);
CREATE INDEX messages_conversation_direction_created_idx ON messages(conversation_id, direction, created_at);
CREATE INDEX messages_status_idx ON messages(status) WHERE status IN ('queued', 'failed');

CREATE FUNCTION verify_conversation_identity_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM contact_identities
    WHERE id = NEW.contact_identity_id
      AND contact_id = NEW.contact_id
      AND channel_id = NEW.channel_id
  ) THEN
    RAISE EXCEPTION 'conversation_channel_mismatch' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER conversations_identity_channel_guard
  BEFORE INSERT OR UPDATE OF contact_id, contact_identity_id, channel_id ON conversations
  FOR EACH ROW EXECUTE FUNCTION verify_conversation_identity_channel();

CREATE FUNCTION verify_message_conversation_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM conversations WHERE id = NEW.conversation_id AND channel_id = NEW.channel_id) THEN
    RAISE EXCEPTION 'conversation_channel_mismatch' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER messages_conversation_channel_guard
  BEFORE INSERT OR UPDATE OF conversation_id, channel_id ON messages
  FOR EACH ROW EXECUTE FUNCTION verify_message_conversation_channel();

CREATE TABLE conversation_read_states (
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE, user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  last_read_message_id uuid REFERENCES messages(id), last_read_at timestamptz NOT NULL, PRIMARY KEY(conversation_id, user_id)
);

CREATE TABLE outbox_events (
  id uuid PRIMARY KEY, aggregate_type text NOT NULL, aggregate_id uuid NOT NULL, event_type text NOT NULL,
  payload jsonb NOT NULL, payload_version smallint NOT NULL DEFAULT 1, correlation_id uuid NOT NULL, causation_id uuid,
  occurred_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), dispatched_at timestamptz,
  CONSTRAINT outbox_event_type_not_blank CHECK (length(btrim(event_type)) > 0)
);
CREATE INDEX outbox_events_undispatched_idx ON outbox_events(created_at) WHERE dispatched_at IS NULL;
CREATE INDEX outbox_events_aggregate_idx ON outbox_events(aggregate_type, aggregate_id, created_at);
