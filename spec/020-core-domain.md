# SPEC-020 Core Domain

Status: ready  
Priority: P0  
Owner: core  
Depends on: SPEC-000, SPEC-010

## 1. Goal

Implement the provider-neutral support domain that all channels and operator workflows use.

The core domain owns:

```text
Channel
Contact
ContactIdentity
Conversation
Message
ConversationReadState
```

The core must not depend on Telegram, VK, MAX, SMTP, IMAP or any provider SDK.

## 2. Product reference

Chatwoot uses a useful conceptual separation between:

```text
Inbox/Channel
Contact
channel-specific Contact identity
Conversation
Message
Attachment
```

and keeps conversation lifecycle, priority, assignment and message direction/status explicit.

This spec keeps those product concepts but intentionally reduces the model for this project's scope.

## 3. Non-goals

Not included in SPEC-020:

- provider webhook parsing;
- durable jobs/outbox implementation (SPEC-030);
- attachment storage (SPEC-040);
- queues/routing (SPEC-100);
- templates;
- workflows;
- SLA policy engine;
- analytics aggregates;
- AI.

SPEC-020 emits domain events through an outbox interface implemented by SPEC-030.

## 4. Domain vocabulary

### Channel

A configured external communication endpoint, for example one Telegram bot, one VK community, one MAX bot or one support mailbox.

### Contact

Canonical internal representation of a person/customer.

### ContactIdentity

The identity of a Contact inside a specific Channel/provider.

### Conversation

Operational support thread for one contact through one channel/external thread.

### Message

Immutable communication record plus mutable delivery metadata.

### Human response

An outgoing message whose actor is an application user (`agent`) and whose delivery status reached `sent` or later.

AI, workflow, system and internal notes are not human responses.

## 5. Core architecture

Recommended module boundaries:

```text
internal/channels/core       Channel configuration/domain contract only
internal/contacts
internal/conversations
internal/messages
internal/events              event contracts only; transport in SPEC-030
```

Provider adapters later live under:

```text
internal/channels/telegram
internal/channels/vk
internal/channels/max
internal/channels/email
```

## 6. Identifiers

Application entities use UUID primary keys.

Conversation also receives a human-readable monotonically increasing `number`.

The number is display-only and is not a security boundary.

## 7. Channel types

```text
telegram
vk
max
email
```

This enum may appear in core as a capability/routing label.

Provider payload types and provider-specific branching may not.

## 8. Channel status

```text
active
degraded
reauthorization_required
disabled
error
```

## 9. SQL schema

The exact migration may vary syntactically, but the constraints and semantics are normative.

### 9.1 Enums

```sql
CREATE TYPE channel_type AS ENUM (
  'telegram',
  'vk',
  'max',
  'email'
);

CREATE TYPE channel_status AS ENUM (
  'active',
  'degraded',
  'reauthorization_required',
  'disabled',
  'error'
);

CREATE TYPE conversation_status AS ENUM (
  'open',
  'pending',
  'resolved',
  'snoozed'
);

CREATE TYPE conversation_priority AS ENUM (
  'low',
  'normal',
  'high',
  'urgent'
);

CREATE TYPE message_direction AS ENUM (
  'incoming',
  'outgoing',
  'internal',
  'system'
);

CREATE TYPE message_actor_type AS ENUM (
  'customer',
  'agent',
  'ai',
  'workflow',
  'system'
);

CREATE TYPE message_content_type AS ENUM (
  'text',
  'html',
  'mixed',
  'attachment_only',
  'unsupported'
);

CREATE TYPE message_status AS ENUM (
  'received',
  'queued',
  'sent',
  'delivered',
  'read',
  'failed'
);
```

### 9.2 Channels

```sql
CREATE TABLE channels (
  id                        uuid PRIMARY KEY,
  type                      channel_type NOT NULL,
  name                      text NOT NULL,
  status                    channel_status NOT NULL DEFAULT 'disabled',
  external_account_id       text,

  config                    jsonb NOT NULL DEFAULT '{}'::jsonb,
  credentials_ciphertext    bytea,
  webhook_secret_ciphertext bytea,

  enabled                   boolean NOT NULL DEFAULT false,

  last_inbound_at           timestamptz,
  last_outbound_at          timestamptz,
  last_success_at           timestamptz,
  last_error_at             timestamptz,
  last_error_code           text,

  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX channels_type_idx ON channels(type);
CREATE INDEX channels_enabled_idx ON channels(enabled) WHERE enabled = true;
```

Provider-specific credential structure is encrypted/decoded only in the adapter/config layer.

### 9.3 Contacts

```sql
CREATE TABLE contacts (
  id                   uuid PRIMARY KEY,
  internal_customer_id text,

  name                 text NOT NULL DEFAULT '',
  email                text,
  phone                text,

  avatar_url           text,
  attributes           jsonb NOT NULL DEFAULT '{}'::jsonb,

  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX contacts_internal_customer_id_uq
  ON contacts(internal_customer_id)
  WHERE internal_customer_id IS NOT NULL;

CREATE INDEX contacts_email_lower_idx
  ON contacts(lower(email))
  WHERE email IS NOT NULL;

CREATE INDEX contacts_phone_idx
  ON contacts(phone)
  WHERE phone IS NOT NULL;
```

Email/phone are not globally unique because unverified third-party data may collide. Identity merging policy belongs to a later refinement of customer context/identity resolution.

### 9.4 Contact identities

```sql
CREATE TABLE contact_identities (
  id                  uuid PRIMARY KEY,
  contact_id          uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  channel_id          uuid NOT NULL REFERENCES channels(id) ON DELETE CASCADE,

  external_user_id    text NOT NULL,
  external_chat_id    text,

  username            text,
  display_name        text,

  metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,

  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT contact_identities_external_user_nonempty
    CHECK (length(trim(external_user_id)) > 0)
);

CREATE UNIQUE INDEX contact_identities_channel_user_uq
  ON contact_identities(channel_id, external_user_id);

CREATE INDEX contact_identities_contact_idx
  ON contact_identities(contact_id);
```

### 9.5 Conversation number sequence

```sql
CREATE SEQUENCE conversation_number_seq;
```

### 9.6 Conversations

`queue_id` is intentionally not present yet; SPEC-100 adds it.

```sql
CREATE TABLE conversations (
  id                  uuid PRIMARY KEY,
  number              bigint NOT NULL DEFAULT nextval('conversation_number_seq'),

  contact_id          uuid NOT NULL REFERENCES contacts(id),
  contact_identity_id uuid NOT NULL REFERENCES contact_identities(id),
  channel_id          uuid NOT NULL REFERENCES channels(id),

  external_thread_id  text NOT NULL,

  assignee_id         uuid REFERENCES users(id),

  status              conversation_status NOT NULL DEFAULT 'open',
  priority            conversation_priority NOT NULL DEFAULT 'normal',

  subject             text,

  waiting_since       timestamptz,
  first_response_at   timestamptz,

  last_inbound_at     timestamptz,
  last_outbound_at    timestamptz,
  last_activity_at    timestamptz NOT NULL,

  resolved_at         timestamptz,
  snoozed_until       timestamptz,

  metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,

  version             bigint NOT NULL DEFAULT 1,

  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT conversations_snooze_consistency CHECK (
    (status = 'snoozed' AND snoozed_until IS NOT NULL)
    OR
    (status <> 'snoozed')
  )
);

CREATE UNIQUE INDEX conversations_number_uq
  ON conversations(number);

CREATE INDEX conversations_thread_lookup_idx
  ON conversations(channel_id, external_thread_id, contact_identity_id, created_at DESC);

CREATE INDEX conversations_contact_idx
  ON conversations(contact_id);

CREATE INDEX conversations_identity_idx
  ON conversations(contact_identity_id);

CREATE INDEX conversations_channel_status_activity_idx
  ON conversations(channel_id, status, last_activity_at DESC);

CREATE INDEX conversations_assignee_status_idx
  ON conversations(assignee_id, status)
  WHERE assignee_id IS NOT NULL;

CREATE INDEX conversations_waiting_idx
  ON conversations(waiting_since)
  WHERE waiting_since IS NOT NULL;
```

Important: a provider may have multiple historical conversations for the same external thread. Therefore `(channel_id, external_thread_id)` is not globally unique. Conversation selection/reuse is an application rule.

### 9.7 Messages

```sql
CREATE TABLE messages (
  id                   uuid PRIMARY KEY,

  conversation_id      uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  channel_id           uuid NOT NULL REFERENCES channels(id),

  external_message_id  text,

  direction            message_direction NOT NULL,
  actor_type           message_actor_type NOT NULL,
  actor_user_id        uuid REFERENCES users(id),

  content_type         message_content_type NOT NULL DEFAULT 'text',
  text_content         text,
  html_content         text,

  reply_to_message_id  uuid REFERENCES messages(id),

  status               message_status NOT NULL,

  external_created_at  timestamptz,
  received_at          timestamptz,
  queued_at            timestamptz,
  sent_at              timestamptz,
  delivered_at         timestamptz,
  read_at              timestamptz,

  error_code           text,
  error_message        text,

  metadata             jsonb NOT NULL DEFAULT '{}'::jsonb,

  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT messages_agent_actor_consistency CHECK (
    (actor_type = 'agent' AND actor_user_id IS NOT NULL)
    OR
    (actor_type <> 'agent' AND actor_user_id IS NULL)
  ),

  CONSTRAINT messages_incoming_status_consistency CHECK (
    direction <> 'incoming'
    OR status = 'received'
  )
);

CREATE UNIQUE INDEX messages_channel_external_id_uq
  ON messages(channel_id, external_message_id)
  WHERE external_message_id IS NOT NULL;

CREATE INDEX messages_conversation_created_idx
  ON messages(conversation_id, created_at, id);

CREATE INDEX messages_conversation_direction_created_idx
  ON messages(conversation_id, direction, created_at);

CREATE INDEX messages_status_idx
  ON messages(status)
  WHERE status IN ('queued', 'failed');
```

### 9.8 Per-user read state

```sql
CREATE TABLE conversation_read_states (
  conversation_id      uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  user_id              uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  last_read_message_id uuid REFERENCES messages(id),
  last_read_at         timestamptz NOT NULL,

  PRIMARY KEY (conversation_id, user_id)
);

CREATE INDEX conversation_read_states_user_idx
  ON conversation_read_states(user_id, last_read_at DESC);
```

## 10. Cross-table invariants

### DATA-CORE-001

A Conversation's `channel_id` must equal its ContactIdentity's `channel_id`.

Enforce in application logic and PostgreSQL integration tests. If a database-level trigger is proposed, it requires an ADR; hidden trigger business logic is not preferred.

### DATA-CORE-002

A Message's `channel_id` must equal its Conversation's `channel_id`.

### DATA-CORE-003

An `actor_user_id` may be set only for `actor_type=agent`.

### DATA-CORE-004

Incoming messages have status `received`.

### DATA-CORE-005

An external message ID is unique within a configured Channel.

### DATA-CORE-006

`last_activity_at` never moves backward.

### DATA-CORE-007

`waiting_since` marks the beginning of the current continuous episode in which the customer is waiting for a successful human response.

## 11. Normalized inbound contract

Core receives provider-neutral input:

```go
type ExternalSender struct {
    ExternalUserID string
    ExternalChatID string
    Username       string
    DisplayName    string
    Metadata       map[string]any
}

type NormalizedInboundMessage struct {
    ProviderEventID   string
    ExternalMessageID string

    ChannelID         uuid.UUID
    ExternalThreadID  string

    Sender            ExternalSender

    Text              string
    HTML              string
    ContentType       MessageContentType

    ReplyToExternalID string

    ExternalCreatedAt time.Time

    Attachments       []ExternalAttachmentRef
    Metadata          map[string]any
}
```

Attachment content itself is handled by SPEC-040.

## 12. Outbound contract

```go
type OutboundMessage struct {
    MessageID          uuid.UUID
    ConversationID     uuid.UUID
    ChannelID          uuid.UUID

    ExternalThreadID   string
    ReplyToExternalID  string

    Text               string
    HTML               string

    Attachments        []OutboundAttachmentRef

    IdempotencyKey     string
    Metadata           map[string]any
}

type SendResult struct {
    ExternalMessageID string
    ProviderStatus    string
    ProviderMetadata  map[string]any
}
```

## 13. Channel adapter contract

Defined in core, implemented only in provider packages:

```go
type ChannelAdapter interface {
    Send(ctx context.Context, msg OutboundMessage) (SendResult, error)
    Capabilities() ChannelCapabilities
}

type ChannelCapabilities struct {
    SupportsHTML          bool
    SupportsReply         bool
    SupportsAttachments   bool
    SupportsDeliveryState bool
    SupportsReadState     bool
    MaxTextLength         int
}
```

Inbound HTTP verification/normalization may use a separate adapter-facing interface in channel specs because it deals with provider request payloads.

## 14. Contact resolution

For one inbound normalized message:

1. look up `(channel_id, external_user_id)`;
2. if found, use existing ContactIdentity and Contact;
3. if not found, create Contact;
4. create ContactIdentity;
5. handle uniqueness races by re-reading the winning identity instead of failing the event.

### FR-CORE-001

Concurrent receipt of two first messages from the same external identity must result in one ContactIdentity.

The implementation may temporarily create and roll back one competing Contact.

## 15. Conversation selection

Conversation lifecycle is intentionally independent from provider thread identity.

For an inbound message:

1. find most recent current conversation for `(channel_id, external_thread_id, contact_identity_id)`;
2. if none, create a new `open` conversation;
3. if latest conversation is `resolved`, reopen it by default for MVP unless a later channel/product rule defines a `new ticket after timeout` policy;
4. if `pending`, incoming customer message changes it to `open`;
5. if `snoozed`, incoming customer message cancels snooze and changes it to `open`.

### FR-CORE-002

A new incoming customer message must never remain hidden only because a conversation was snoozed.

## 16. Conversation state machine

Allowed explicit operator transitions:

```text
open -> pending
open -> resolved
open -> snoozed

pending -> open
pending -> resolved
pending -> snoozed

snoozed -> open
snoozed -> resolved

resolved -> open
```

Invalid transitions return a domain error.

### Status semantics

#### open

Support action may be required.

#### pending

Support is waiting for customer or an external dependency.

#### snoozed

Conversation is intentionally deferred until `snoozed_until`.

#### resolved

Current issue is considered complete.

## 17. Automatic state transitions on inbound customer message

```text
open      -> open
pending   -> open
snoozed   -> open
resolved  -> open
```

When leaving snoozed:

```text
snoozed_until = NULL
```

When reopening resolved:

```text
resolved_at = NULL
```

Emit distinct event:

```text
conversation.reopened
```

## 18. Snooze wakeup

Timer-based wakeup is implemented later through SPEC-030 scheduled jobs.

Core exposes:

```go
func (s *ConversationService) WakeSnoozed(ctx context.Context, conversationID uuid.UUID, now time.Time) error
```

It changes `snoozed -> open` only if:

- status is still snoozed;
- `snoozed_until <= now`.

The operation is idempotent.

## 19. Priority transitions

Any authorized operator may set:

```text
low
normal
high
urgent
```

No implicit status change occurs when priority changes.

## 20. Incoming message transaction

Normative transaction:

```text
BEGIN

resolve/create ContactIdentity
resolve/create Contact
lock/select current Conversation
create or transition Conversation
insert Message
update Conversation timestamps/waiting state
insert domain events through OutboxWriter

COMMIT
```

### FR-CORE-003

The message and corresponding conversation state transition must be atomic.

### FR-CORE-004

A crash must not produce a persisted Message without its required Conversation state update/event.

## 21. Waiting episode algorithm

On incoming customer message at `T`:

```text
if conversation.waiting_since IS NULL:
    waiting_since = T

last_inbound_at = max(last_inbound_at, T)
last_activity_at = max(last_activity_at, T)
```

If another customer message arrives before a successful human reply:

```text
waiting_since remains unchanged
```

Example:

```text
10:00 customer message -> waiting_since=10:00
10:05 customer message -> waiting_since=10:00
10:09 customer message -> waiting_since=10:00
```

### DATA-CORE-008

Waiting start is not the timestamp of the latest customer message.

## 22. Successful human reply algorithm

An outbound agent Message is created initially as `queued`.

It does not stop customer waiting yet.

When delivery worker confirms provider acceptance and message transitions to `sent`:

```text
if actor_type == agent:
    if first_response_at IS NULL:
        first_response_at = sent_at
    waiting_since = NULL
    last_outbound_at = max(last_outbound_at, sent_at)
    last_activity_at = max(last_activity_at, sent_at)
```

### FR-CORE-005

A `failed` agent message does not clear `waiting_since`.

### FR-CORE-006

AI/workflow/system outgoing messages do not clear `waiting_since` in MVP.

## 23. Message status state machine

Incoming:

```text
received
```

Outgoing:

```text
queued -> sent -> delivered -> read
queued -> failed
failed -> queued
sent -> failed        only when provider later reports definitive send failure, if supported
```

Skipping unsupported states is allowed:

```text
queued -> sent
```

A channel without delivery/read receipts remains at `sent`.

## 24. Message immutability

After creation:

Normally immutable:

```text
conversation_id
channel_id
direction
actor_type
actor_user_id
text_content
html_content
reply_to_message_id
external_created_at
```

Mutable operational fields:

```text
status
external_message_id
sent_at
delivered_at
read_at
error_code
error_message
provider metadata subset
```

Editing sent customer-facing content is not part of MVP.

## 25. Message creation commands

Suggested application contracts:

```go
type ReceiveInboundCommand struct {
    Input NormalizedInboundMessage
    Now   time.Time
}

type QueueOutboundCommand struct {
    ConversationID    uuid.UUID
    Actor              Principal
    Text               string
    HTML               string
    ReplyToMessageID   *uuid.UUID
    IdempotencyKey     string
    Now                time.Time
}

type MarkMessageSentCommand struct {
    MessageID          uuid.UUID
    ExternalMessageID  string
    SentAt             time.Time
    ProviderMetadata   map[string]any
}

type MarkMessageFailedCommand struct {
    MessageID    uuid.UUID
    FailedAt     time.Time
    ErrorCode    string
    ErrorMessage string
}
```

## 26. Idempotency for outbound API creation

Add a durable table or equivalent unique record for client idempotency keys:

```sql
CREATE TABLE message_idempotency_keys (
  user_id          uuid NOT NULL REFERENCES users(id),
  idempotency_key  text NOT NULL,
  request_hash     text NOT NULL,
  message_id       uuid NOT NULL REFERENCES messages(id),
  created_at       timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (user_id, idempotency_key)
);
```

### FR-CORE-007

Same user + same idempotency key + same request -> return same Message.

### FR-CORE-008

Same user + same idempotency key + different request body -> return conflict, never create a second message.

Retention of old keys may be configured later but must be long enough to cover expected client retries.

## 27. Domain errors

Stable examples:

```text
channel_not_found
channel_disabled
contact_not_found
conversation_not_found
message_not_found

invalid_conversation_transition
invalid_message_transition

duplicate_external_message
idempotency_conflict

conversation_channel_mismatch
contact_identity_channel_mismatch

validation_failed
```

## 28. Domain events contract

Core uses an injected interface:

```go
type Event struct {
    ID            uuid.UUID
    Type          string
    AggregateType string
    AggregateID   uuid.UUID
    OccurredAt    time.Time
    CorrelationID uuid.UUID
    CausationID   *uuid.UUID
    Payload       json.RawMessage
}

type OutboxWriter interface {
    Append(ctx context.Context, tx pgx.Tx, events ...Event) error
}
```

Core may not publish directly to Redis or an external broker.

## 29. Events emitted by SPEC-020

At minimum:

```text
contact.created
contact_identity.created

conversation.created
conversation.updated
conversation.opened
conversation.pending
conversation.snoozed
conversation.resolved
conversation.reopened
conversation.priority_changed
conversation.assigned

message.received
message.queued
message.sent
message.delivered
message.read
message.failed

conversation.read_state_changed
```

Payloads must contain stable IDs, not entire secret-bearing models.

## 30. Conversation event examples

`conversation.reopened`:

```json
{
  "conversation_id": "...",
  "previous_status": "resolved",
  "status": "open",
  "reason": "customer_message"
}
```

`message.received`:

```json
{
  "message_id": "...",
  "conversation_id": "...",
  "channel_id": "...",
  "contact_id": "...",
  "external_created_at": "..."
}
```

Message text is not required in domain event payloads; consumers can fetch it when necessary. This reduces PII propagation.

## 31. Concurrency strategy

### Conversation mutation

For state transitions driven by messages:

```sql
SELECT ...
FROM conversations
WHERE id = $1
FOR UPDATE;
```

or equivalent locked selection inside the business transaction.

### DATA-CORE-009

Two concurrent inbound messages must not move `waiting_since` forward incorrectly.

### DATA-CORE-010

Concurrent resolve + inbound customer message must result in a deterministic valid final state according to lock order.

Recommended rule:

- whichever transaction locks first commits;
- the second re-evaluates current state after acquiring the lock;
- inbound message ultimately leaves the conversation open.

## 32. Conversation version

`version` increments on every business-relevant conversation update.

Frontend may later use it for stale update detection.

### DATA-CORE-011

Version increments atomically with state mutation.

## 33. Read/unread semantics

Read state is per user.

### Mark read

```text
last_read_message_id = newest visible message
last_read_at = now
```

### Unread count

Count incoming customer messages after `last_read_at` or after the referenced message ordering boundary.

Implementation must define one consistent ordering rule based on `(created_at, id)`.

Do not use only wall-clock timestamps as a strict ordering key.

## 34. Application service interfaces

Suggested:

```go
type ConversationService interface {
    Get(ctx context.Context, principal Principal, id uuid.UUID) (Conversation, error)
    ChangeStatus(ctx context.Context, principal Principal, id uuid.UUID, to ConversationStatus, snoozeUntil *time.Time) (Conversation, error)
    ChangePriority(ctx context.Context, principal Principal, id uuid.UUID, to ConversationPriority) (Conversation, error)
    Assign(ctx context.Context, principal Principal, id uuid.UUID, assigneeID *uuid.UUID) (Conversation, error)
    MarkRead(ctx context.Context, principal Principal, id uuid.UUID, messageID uuid.UUID, at time.Time) error
}

type MessageService interface {
    ReceiveInbound(ctx context.Context, cmd ReceiveInboundCommand) (Message, Conversation, error)
    QueueOutbound(ctx context.Context, cmd QueueOutboundCommand) (Message, error)
    MarkSent(ctx context.Context, cmd MarkMessageSentCommand) error
    MarkDelivered(ctx context.Context, messageID uuid.UUID, at time.Time) error
    MarkRead(ctx context.Context, messageID uuid.UUID, at time.Time) error
    MarkFailed(ctx context.Context, cmd MarkMessageFailedCommand) error
}
```

Authorization for operator commands uses SPEC-010.

Inbound system commands are authenticated at the channel ingress boundary rather than as a human Principal.

## 35. Repository boundaries

Suggested repositories:

```go
type ContactRepository interface { /* use-case-specific methods */ }
type ContactIdentityRepository interface { /* use-case-specific methods */ }
type ConversationRepository interface { /* use-case-specific methods */ }
type MessageRepository interface { /* use-case-specific methods */ }
type ReadStateRepository interface { /* use-case-specific methods */ }
```

Avoid a generic repository abstraction.

SQL queries should reflect domain use cases.

## 36. Search/listing

Complex Inbox filtering belongs to SPEC-090.

SPEC-020 must provide efficient primitives:

```text
conversation by id
conversation by number
messages page by conversation
contact identities by contact
current conversation candidate by channel/thread/identity
```

Message pagination should be cursor-ready using `(created_at, id)`.

## 37. Security and privacy

### SEC-CORE-001

Provider credentials in Channel records are never included in domain DTOs returned to normal operator APIs.

### SEC-CORE-002

Message HTML is treated as untrusted. Sanitization rules are implemented by the Email/UI specs before rendering.

### SEC-CORE-003

Message content must not be automatically written to structured application logs.

### SEC-CORE-004

Domain events should prefer IDs/classification metadata over full message text.

## 38. Observability

Metrics:

```text
contacts_created_total
contact_identity_conflicts_total

conversations_created_total
conversation_status_changes_total{from,to}

messages_created_total{direction,actor_type}
message_status_changes_total{from,to}
duplicate_external_messages_total

conversation_waiting_started_total
conversation_waiting_cleared_total
```

Spans:

```text
core.receive_inbound
core.queue_outbound
core.message_mark_sent
core.conversation_change_status
```

Do not attach full message text to span attributes.

## 39. Acceptance criteria

### AC-CORE-001

A Contact can have Telegram and VK identities simultaneously.

### AC-CORE-002

Two identities from different channels may have the same `external_user_id` without conflict.

### AC-CORE-003

Two identities in the same channel cannot have the same `external_user_id`.

### AC-CORE-004

First inbound message creates or resolves a ContactIdentity and creates/uses a Conversation.

### AC-CORE-005

Ten sequential deliveries with the same `(channel_id, external_message_id)` persist one Message.

The caller receives an idempotent/duplicate-safe outcome.

### AC-CORE-006

Ten concurrent first messages from the same external identity produce one ContactIdentity.

### AC-CORE-007

Inbound message to a pending conversation changes it to open.

### AC-CORE-008

Inbound message to a snoozed conversation changes it to open and clears `snoozed_until`.

### AC-CORE-009

Inbound message to a resolved conversation reopens it.

### AC-CORE-010

First customer message at 10:00 sets `waiting_since=10:00`.

### AC-CORE-011

Additional customer messages at 10:05 and 10:09 leave `waiting_since=10:00`.

### AC-CORE-012

Queued agent reply does not clear waiting.

### AC-CORE-013

Failed agent reply does not clear waiting.

### AC-CORE-014

Successful agent message sent at 10:12 clears waiting and sets first response to 10:12 if it is the first human response.

### AC-CORE-015

AI/workflow/system message does not clear waiting.

### AC-CORE-016

Second successful human response does not overwrite `first_response_at`.

### AC-CORE-017

Resolved conversation can reopen and start a new waiting episode without changing historical event records.

### AC-CORE-018

Same user + same outbound Idempotency-Key + same request returns same Message.

### AC-CORE-019

Same Idempotency-Key with a different request returns conflict.

### AC-CORE-020

Read state for user A does not mark conversation read for user B.

### AC-CORE-021

Conversation `version` increases on state mutation.

### AC-CORE-022

Domain core packages contain no imports from Telegram/VK/MAX provider SDK packages.

## 40. Mandatory tests

### State machine table tests

All allowed and forbidden status transitions.

### Waiting episode golden tests

```text
one inbound -> one sent human reply
multiple inbound -> one sent reply
failed reply -> retry -> sent
AI reply before human
workflow reply before human
pending -> inbound
snoozed -> inbound
resolved -> inbound
```

### Database integration

- FK/unique constraints;
- channel identity race;
- duplicate external message race;
- outbound idempotency-key race;
- `FOR UPDATE` concurrency.

### Transaction failure injection

Force an error:

- after Message insert but before Conversation update;
- after Conversation update but before OutboxWriter append;
- during OutboxWriter append.

Expected: entire business transaction rolls back.

### Package boundary test

CI/script verifies provider packages are not imported from core directories.

## 41. Task decomposition

```text
020-01 enum/domain value objects
020-02 SQL migrations
020-03 Channel model/repository
020-04 Contact model/repository
020-05 ContactIdentity + race-safe resolution
020-06 Conversation repository + locking
020-07 Conversation state machine
020-08 Message repository + status machine
020-09 normalized inbound/outbound contracts
020-10 ReceiveInbound service
020-11 QueueOutbound service
020-12 MarkSent/Delivered/Read/Failed
020-13 waiting episode logic
020-14 outbound idempotency keys
020-15 read state
020-16 domain event contracts
020-17 metrics/tracing
020-18 transaction failure tests
020-19 concurrency/race tests
020-20 package-boundary verification
```

## 42. Codex implementation task

```text
Implement SPEC-020 Core Domain.

This is a correctness-critical spec.

Core must remain channel/provider neutral.

Implement the schema, explicit state machines, repositories and application
services for:
Channel
Contact
ContactIdentity
Conversation
Message
ConversationReadState

Critical invariants:
- same provider external message cannot create duplicates;
- concurrent first messages cannot create duplicate ContactIdentity;
- incoming customer messages reopen pending/snoozed/resolved conversations;
- waiting_since marks the first unanswered customer message in the current
  waiting episode;
- queued/failed/AI/workflow/system messages do not clear waiting_since;
- only a successfully sent human agent response clears waiting;
- first_response_at is set once;
- message + conversation mutation + outbox append are one DB transaction;
- same outbound Idempotency-Key cannot create two messages;
- all provider SDK/types remain outside core.

Use PostgreSQL locking/constraints as final concurrency guards.

Required verification:
- full state-machine table tests
- waiting-episode golden tests
- duplicate message sequential/concurrent tests
- ContactIdentity creation race test
- Idempotency-Key race/conflict tests
- transaction failure-injection tests
- go test -race
- package boundary test
- no message content in logs/traces
```
