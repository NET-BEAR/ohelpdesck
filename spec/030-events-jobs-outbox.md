# SPEC-030 Durable Events, Jobs and Transactional Outbox

Status: ready  
Priority: P0  
Owner: platform/core  
Depends on: SPEC-000, SPEC-020

## 1. Goal

Provide durable asynchronous execution with:

- raw inbound event persistence;
- PostgreSQL-backed jobs;
- scheduling;
- lease-based multi-worker execution;
- retry with exponential backoff and jitter;
- dead-letter state;
- transactional outbox;
- durable event fan-out;
- handler idempotency;
- crash recovery.

The system must remain correct under at-least-once delivery and process crashes.

## 2. Non-goals

Not included:

- provider-specific webhook payload parsing;
- Telegram/VK/MAX/Email adapters;
- Kafka;
- exactly-once external side effects;
- distributed workflow engine;
- analytics implementation;
- SLA implementation.

## 3. Architecture decisions

### ADR-EVT-001 PostgreSQL-backed durability

PostgreSQL stores:

```text
inbound_events
outbox_events
jobs
event_handler_receipts
```

Redis is not used as the durable queue.

### ADR-EVT-002 Worker leasing

Workers claim jobs using PostgreSQL:

```sql
FOR UPDATE SKIP LOCKED
```

with a lease expiration.

### ADR-EVT-003 At-least-once

Job execution is at least once.

Correctness requires:

- unique keys;
- transactional handlers;
- handler receipts;
- provider idempotency/reconciliation for external side effects.

### ADR-EVT-004 Transactional outbox

Domain state and the event describing it are committed in the same database transaction.

### ADR-EVT-005 Durable fan-out

One domain event may have multiple consumers.

The outbox dispatcher creates one durable job per registered durable handler and marks the outbox event dispatched only after those jobs exist.

A unique `(event_id, handler)` constraint prevents duplicate fan-out.

## 4. Concepts

### Inbound Event

Raw provider callback/update stored before provider acknowledgement.

### Domain Event

A fact produced by a committed business transaction.

### Job

A durable unit of asynchronous work.

### Handler

Named code path consuming a job or domain event.

### Lease

Temporary ownership of a job by one worker.

### Handler Receipt

Durable record proving that one handler has transactionally consumed one domain event.

## 5. Inbound event schema

```sql
CREATE TYPE inbound_event_status AS ENUM (
  'received',
  'processing',
  'processed',
  'unsupported',
  'failed',
  'dead'
);

CREATE TABLE inbound_events (
  id                  uuid PRIMARY KEY,

  channel_id          uuid NOT NULL REFERENCES channels(id),
  provider_event_id   text NOT NULL,
  dedup_key           text NOT NULL,

  event_type_hint     text,

  payload             jsonb NOT NULL,
  headers             jsonb NOT NULL DEFAULT '{}'::jsonb,

  status              inbound_event_status NOT NULL DEFAULT 'received',

  attempt_count       integer NOT NULL DEFAULT 0,
  last_error_code     text,
  last_error_message  text,

  received_at         timestamptz NOT NULL,
  processing_started_at timestamptz,
  processed_at        timestamptz,

  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX inbound_events_channel_dedup_uq
  ON inbound_events(channel_id, dedup_key);

CREATE INDEX inbound_events_status_received_idx
  ON inbound_events(status, received_at)
  WHERE status IN ('received', 'failed');
```

### DATA-EVT-001

`dedup_key` must be deterministic for the same provider event.

Prefer provider event ID. If unavailable, the channel adapter defines a canonical hash input.

### SEC-EVT-001

Stored headers must be allowlisted/redacted. Authorization headers and provider tokens must not be persisted.

## 6. Job schema

```sql
CREATE TYPE job_status AS ENUM (
  'pending',
  'running',
  'completed',
  'dead',
  'cancelled'
);

CREATE TABLE jobs (
  id                  uuid PRIMARY KEY,

  type                text NOT NULL,
  handler             text NOT NULL,

  payload             jsonb NOT NULL DEFAULT '{}'::jsonb,

  status              job_status NOT NULL DEFAULT 'pending',

  priority            integer NOT NULL DEFAULT 100,
  run_at              timestamptz NOT NULL DEFAULT now(),

  attempts            integer NOT NULL DEFAULT 0,
  max_attempts        integer NOT NULL DEFAULT 10,

  dedup_key           text,
  event_id            uuid,

  locked_by           text,
  locked_at           timestamptz,
  lease_expires_at    timestamptz,
  lease_token         uuid,

  last_error_code     text,
  last_error_message  text,

  correlation_id      uuid,
  causation_id        uuid,

  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  completed_at        timestamptz,

  CONSTRAINT jobs_running_lease_consistency CHECK (
    (status = 'running' AND locked_by IS NOT NULL AND lease_expires_at IS NOT NULL AND lease_token IS NOT NULL)
    OR
    (status <> 'running')
  )
);

CREATE INDEX jobs_claim_idx
  ON jobs(priority, run_at, created_at)
  WHERE status = 'pending';

CREATE INDEX jobs_running_lease_idx
  ON jobs(lease_expires_at)
  WHERE status = 'running';

CREATE INDEX jobs_dead_idx
  ON jobs(created_at)
  WHERE status = 'dead';

CREATE UNIQUE INDEX jobs_type_dedup_active_uq
  ON jobs(type, dedup_key)
  WHERE dedup_key IS NOT NULL AND status IN ('pending', 'running');

CREATE UNIQUE INDEX jobs_event_handler_uq
  ON jobs(event_id, handler)
  WHERE event_id IS NOT NULL;
```

The active dedup index is a convenience, not a replacement for business idempotency.

## 7. Outbox schema

```sql
CREATE TABLE outbox_events (
  id              uuid PRIMARY KEY,

  event_type      text NOT NULL,
  aggregate_type  text NOT NULL,
  aggregate_id    uuid NOT NULL,

  payload         jsonb NOT NULL,

  occurred_at     timestamptz NOT NULL,

  correlation_id  uuid NOT NULL,
  causation_id    uuid,

  dispatched_at   timestamptz,

  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX outbox_events_undispatched_idx
  ON outbox_events(created_at)
  WHERE dispatched_at IS NULL;

CREATE INDEX outbox_events_aggregate_idx
  ON outbox_events(aggregate_type, aggregate_id, occurred_at);
```

## 8. Durable event handler receipts

```sql
CREATE TABLE event_handler_receipts (
  event_id       uuid NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
  handler        text NOT NULL,
  processed_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY(event_id, handler)
);
```

Use for handlers whose effects can be committed in PostgreSQL in the same transaction.

## 9. Event-handler job fan-out

For an event:

```text
message.received
```

registered handlers might later be:

```text
workflow.message_received
sla.message_received
analytics.message_received
realtime.message_received
customer_context.message_received
```

The dispatcher transaction:

```text
BEGIN

SELECT undispatched outbox event FOR UPDATE SKIP LOCKED

for each registered durable handler:
    INSERT job(
      type='domain_event',
      handler=<handler>,
      payload={event_id},
      event_id=<event_id>
    )
    -- unique(event_id, handler) makes replay safe

UPDATE outbox_events
SET dispatched_at=now()
WHERE id=<event_id>

COMMIT
```

## 10. Job claim algorithm

Pseudo-SQL:

```sql
WITH candidate AS (
  SELECT id
  FROM jobs
  WHERE status = 'pending'
    AND run_at <= now()
  ORDER BY priority ASC, run_at ASC, created_at ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
UPDATE jobs j
SET
  status = 'running',
  locked_by = $2,
  locked_at = now(),
  lease_expires_at = now() + $3::interval,
  lease_token = gen_random_uuid(),
  attempts = attempts + 1,
  updated_at = now()
FROM candidate
WHERE j.id = candidate.id
RETURNING j.*;
```

### DATA-EVT-002

Only one active worker lease may own a given job row at a time.

## 11. Lease duration

Lease duration is handler-aware/configurable.

A worker may extend a lease for long operations.

### FR-EVT-001

A worker must not keep a job permanently running after process death.

### FR-EVT-002

Expired running jobs are recoverable.

Recovery operation:

```sql
UPDATE jobs
SET
  status='pending',
  locked_by=NULL,
  locked_at=NULL,
  lease_expires_at=NULL,
  lease_token=NULL,
  run_at=now(),
  updated_at=now()
WHERE status='running'
  AND lease_expires_at < now();
```

The operation itself must be concurrency-safe.

## 12. Worker identity

Each worker process has a unique runtime ID, for example:

```text
hostname:pid:random
```

It is diagnostic only, not security-sensitive.

## 13. Retry classification

Handlers return typed failure:

```go
type FailureClass string

const (
    FailureTransient FailureClass = "transient"
    FailurePermanent FailureClass = "permanent"
)

type JobError struct {
    Class      FailureClass
    Code       string
    Message    string
    RetryAfter *time.Duration
    Cause      error
}
```

### Transient examples

```text
timeout
temporary DNS/network error
HTTP 429
HTTP 5xx
database serialization failure
temporary object storage error
```

### Permanent examples

```text
invalid recipient
authorization revoked when no refresh path exists
unsupported payload
validation error
deleted/forbidden resource
malformed configuration
```

Provider adapters refine mappings in channel specs.

## 14. Backoff

Default retry delay conceptually:

```text
base * 2^(attempt-1)
```

bounded by configured maximum, with jitter.

If provider gives a trustworthy `Retry-After`, respect it within safe configured bounds.

### NFR-EVT-001

Workers must not retry all failed jobs at the exact same instant after a dependency outage.

## 15. Dead-letter behavior

A job moves to `dead` when:

- failure is permanent; or
- attempts reaches `max_attempts`.

Record:

```text
last_error_code
last_error_message
```

No stack trace is persisted in user-facing job state.

### FR-EVT-003

Dead jobs remain queryable and may be manually retried through an admin operation introduced later.

Manual retry creates/requeues work while preserving original failure history in logs/telemetry.

## 16. Job handler registry

Do not dispatch jobs by reflection or arbitrary function names from untrusted data.

Use explicit registry:

```go
type Handler interface {
    Handle(ctx context.Context, job Job) error
}

type Registry interface {
    Resolve(name string) (Handler, bool)
}
```

Unknown handler -> permanent configuration error and alert.

## 17. Scheduled jobs

A future operation is simply:

```text
status=pending
run_at=<future timestamp>
```

Use the same job infrastructure for:

- snooze wakeup;
- delayed workflow;
- SLA timers;
- retries.

No separate cron framework is required for one-shot durable timers.

Periodic maintenance may enqueue jobs from a small scheduler loop.

## 18. Inbound webhook acceptance transaction

Provider-specific HTTP layer later performs signature verification before persistence when the signature can be verified from the raw body.

Normative acceptance:

```text
verify request
BEGIN
INSERT inbound_event (dedup-safe)
INSERT job(type='inbound_event', handler=<provider normalizer>, event id)
COMMIT
return provider-required success
```

### FR-EVT-004

Provider success must not be returned before the inbound event is durably committed, except where a provider's protocol explicitly requires a different handshake that is separately specified.

### FR-EVT-005

Duplicate inbound callback returns success after confirming the existing durable event instead of creating duplicate work.

## 19. Inbound event processing

Handler flow:

```text
load inbound_event
transition received/failed -> processing
provider adapter normalize
core.ReceiveInbound(...)
mark processed
```

All provider-specific parsing is in later channel specs.

### Unsupported event

Known-valid provider event outside MVP capability:

```text
status = unsupported
```

No automatic infinite retry.

Metric increments.

### Malformed/invalid event

If signature passed but payload is invalid:

```text
status = failed or dead
```

according to provider contract and error classification.

## 20. Domain transaction and outbox

Core services receive `OutboxWriter`.

Example:

```go
err := txManager.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
    msg, conv, events, err := domain.Apply(...)
    if err != nil {
        return err
    }

    if err := repos.Save(ctx, tx, msg, conv); err != nil {
        return err
    }

    return outbox.Append(ctx, tx, events...)
})
```

### DATA-EVT-003

There is no code path that commits a business mutation and publishes its event only afterward outside the transaction.

## 21. Outbox dispatcher

The dispatcher is a worker role/loop inside `support-worker`.

It must:

- process in bounded batches;
- use `SKIP LOCKED`;
- be safe with multiple workers;
- create durable handler jobs;
- mark event dispatched only after durable jobs exist;
- emit lag metrics.

### OBS-EVT-001

Measure oldest undispatched outbox event age.

## 22. Consumer transaction pattern

For DB-only event handlers:

```text
BEGIN
check event_handler_receipts(event_id, handler)

if exists:
   COMMIT / success

perform handler DB changes
insert event_handler_receipt

COMMIT
```

### DATA-EVT-004

Receipt and handler DB effects must be atomic.

This prevents duplicate effects when a worker crashes after handler commit but before job completion.

## 23. External side effects

A generic event receipt cannot make a third-party network operation exactly once.

For outbound message sending, later channel specs must use:

1. stable Message ID/idempotency key;
2. provider-native idempotency where available;
3. durable attempt record;
4. reconciliation when provider outcome is ambiguous.

### NFR-EVT-002

The architecture must never claim exactly-once delivery to external providers unless the provider contract proves it.

## 24. Job completion

After successful handler transaction:

```sql
UPDATE jobs
SET
  status='completed',
  completed_at=now(),
  locked_by=NULL,
  locked_at=NULL,
  lease_expires_at=NULL,
  lease_token=NULL,
  updated_at=now()
WHERE id=$1
  AND status='running'
  AND locked_by=$2
  AND lease_token=$3;
```

If completion update fails after the handler committed, the job may be retried; handler idempotency/receipt makes this safe for supported handler types.

## 25. Lease ownership check

A worker may complete/reschedule only a job it currently owns.

### DATA-EVT-005

A stale worker whose lease expired must not overwrite the state created by a newer worker lease.

A unique `lease_token` is set on each claim and required on completion/retry/lease extension.

## 26. Lease extension

A running worker may extend its lease only if:

```text
job.id matches
status=running
locked_by matches
lease_token matches
lease has not already been replaced
```

Extension is bounded. A stuck handler cannot extend forever without process-level health implications.

## 27. Cancellation

Jobs may be cancelled only while `pending`, except spec-specific cooperative cancellation.

A cancelled job is never leased.

Use cases later:

- delayed workflow invalidated by rule change;
- superseded scheduled operation.

## 28. Priority

Lower numeric value means higher scheduling priority.

Suggested defaults:

```text
critical  10
high      50
normal    100
low       200
```

Do not create one physical queue table per priority.

## 29. Handler isolation

A failing low-priority handler must not permanently starve high-priority work.

Workers may support concurrency limits per handler class later, but MVP must at least order by priority and maintain bounded batches.

## 30. Graceful worker shutdown

On shutdown signal:

1. stop claiming jobs;
2. allow active handlers to finish within grace timeout;
3. do not extend leases after shutdown deadline;
4. if handler cannot finish, exit and allow lease recovery;
5. flush telemetry.

Do not mark unfinished jobs completed.

## 31. Worker concurrency

Concurrency is configurable per process.

### NFR-EVT-003

One process may run multiple handler goroutines, but must maintain bounded concurrency and backpressure.

No unbounded goroutine per job.

## 32. Database connection budget

Worker concurrency and API pool sizes must respect PostgreSQL connection budget.

Document deployment sizing conceptually as:

```text
(total API replicas * API max conns)
+
(total worker replicas * worker max conns)
+
operational headroom
< PostgreSQL connection limit / pooler policy
```

Do not set worker concurrency independently of DB pool configuration.

## 33. Payload policy

### SEC-EVT-002

Job/outbox payloads must not contain secrets.

### SEC-EVT-003

Prefer stable entity IDs to full message/customer objects.

### SEC-EVT-004

Raw inbound payload retention must be configurable because it may contain PII.

Initial default target:

```text
30 days
```

Retention cleanup is a later scheduled maintenance task.

## 34. Error storage policy

Persist:

```text
error_code
sanitized error_message
```

Logs/traces may contain developer diagnostics, still without secrets/message bodies unless explicitly safe.

## 35. Correlation and causation

Every domain event:

```text
event_id
correlation_id
causation_id
```

Rules:

- first external inbound event starts a new correlation ID;
- downstream events preserve correlation ID;
- causation ID points to the immediate preceding event/command where applicable.

Jobs copy correlation/causation context.

This is required for workflow recursion control later.

## 36. Public Go interfaces

### Queue

```go
type EnqueueRequest struct {
    Type          string
    Handler       string
    Payload       json.RawMessage
    Priority      int
    RunAt         time.Time
    MaxAttempts   int
    DedupKey      string
    EventID       *uuid.UUID
    CorrelationID *uuid.UUID
    CausationID   *uuid.UUID
}

type JobQueue interface {
    Enqueue(ctx context.Context, tx pgx.Tx, req EnqueueRequest) (Job, error)
}
```

Queue writes may participate in an existing business transaction.

### Outbox

```go
type OutboxWriter interface {
    Append(ctx context.Context, tx pgx.Tx, events ...events.Event) error
}
```

### Transactional event consumer

```go
type EventHandler interface {
    HandleEvent(ctx context.Context, tx pgx.Tx, event events.Event) error
}
```

A wrapper handles receipt checks and transaction ownership.

## 37. Inbound event interface

```go
type StoreInboundRequest struct {
    ChannelID       uuid.UUID
    ProviderEventID string
    DedupKey        string
    EventTypeHint   string
    Payload         json.RawMessage
    SafeHeaders     map[string]string
    ReceivedAt      time.Time
    Handler         string
}

type InboundEventStore interface {
    StoreAndEnqueue(ctx context.Context, req StoreInboundRequest) (InboundEvent, bool /* duplicate */, error)
}
```

Store + enqueue must be atomic.

## 38. Failure injection points

Tests must be able to inject failures at:

```text
F1 before business transaction commit
F2 after business rows written, before outbox insert
F3 after outbox insert, before commit
F4 after business commit, before outbox dispatch
F5 after first handler job insert, before dispatcher commit
F6 after handler DB effects, before receipt insert
F7 after handler+receipt commit, before job completed update
F8 after external request transmitted, before provider response observed
F9 after job claim, before handler starts
F10 during graceful shutdown
```

Not every failure can be solved generically, but expected behavior must be documented and tested where possible.

## 39. Required crash guarantees

### F1/F2/F3

No partial business transaction is visible.

### F4

Outbox event remains undispatched and is later processed.

### F5

Dispatcher transaction rolls back or unique constraints make replay safe.

### F6

Handler transaction rolls back including effects.

### F7

Job may run again; receipt makes DB-only handler a no-op.

### F8

Outcome is ambiguous. Channel-specific idempotency/reconciliation handles it; generic queue must not assume failure means `not sent`.

### F9

Lease expires and another worker can claim.

### F10

Completed work remains completed; incomplete leased work recovers after lease expiry.

## 40. Metrics

Required:

```text
jobs_enqueued_total{handler}
jobs_started_total{handler}
jobs_completed_total{handler}
jobs_failed_total{handler,class,code}
jobs_dead_total{handler}
jobs_retried_total{handler}
job_duration_seconds{handler}
job_wait_seconds{handler}
job_lease_expired_total{handler}

job_queue_depth{handler,status}
oldest_pending_job_age_seconds{handler}

outbox_events_created_total{event_type}
outbox_events_dispatched_total{event_type}
outbox_dispatch_lag_seconds
outbox_undispatched_count

inbound_events_total{channel_type,status}
inbound_duplicate_events_total{channel_type}
inbound_processing_seconds{channel_type}
```

Avoid high-cardinality IDs in metric labels.

## 41. Tracing

Spans:

```text
job.claim
job.handle
job.retry
outbox.dispatch
inbound.store
inbound.process
```

Span attributes may include:

```text
job.handler
job.attempt
event.type
channel.type
```

Do not include full payload/message text.

## 42. Alerts enabled by this spec

Later deployment should be able to alert on:

```text
oldest pending critical job age
dead jobs > 0 for critical handlers
outbox dispatch lag
inbound processing backlog
lease expiration spike
retry spike
```

## 43. Acceptance criteria

### AC-EVT-001

An inbound event and its processing job are committed atomically.

### AC-EVT-002

Ten sequential duplicate inbound events with the same dedup key create one inbound event.

### AC-EVT-003

Ten concurrent duplicate inbound events with the same dedup key create one inbound event and one active normalization job.

### AC-EVT-004

Two workers never successfully lease the same job at the same time.

### AC-EVT-005

Worker death after claim causes job recovery after lease expiry.

### AC-EVT-006

Transient failure increments attempt count and schedules future retry with jitter.

### AC-EVT-007

Permanent failure goes dead without exhausting pointless retries.

### AC-EVT-008

Max attempts exceeded -> dead.

### AC-EVT-009

A business transaction rollback leaves no outbox event.

### AC-EVT-010

A committed business transaction always leaves its outbox event even if worker is down.

### AC-EVT-011

Two outbox dispatchers can run concurrently without duplicate durable handler jobs.

### AC-EVT-012

Crash after handler DB commit but before job-complete update does not repeat DB effect due to handler receipt.

### AC-EVT-013

Expired stale worker cannot complete a job owned by a newer lease token.

### AC-EVT-014

Scheduled job is not claimed before `run_at`.

### AC-EVT-015

Cancelled pending job is never executed.

### AC-EVT-016

Unknown handler becomes an observable permanent failure, not silent success.

### AC-EVT-017

Job payload/error/log persistence contains no configured test secret.

### AC-EVT-018

Outbox lag and queue depth metrics are exposed.

### AC-EVT-019

Graceful worker shutdown stops new claims and does not lose pending work.

### AC-EVT-020

`go test -race` passes for worker/registry/lease logic.

## 44. Mandatory tests

### PostgreSQL concurrency tests

- `SKIP LOCKED` with at least two workers;
- duplicate enqueue race;
- inbound dedup race;
- outbox dispatcher race;
- lease expiry/reclaim race;
- stale lease-token completion rejection.

### Retry tests

- transient;
- permanent;
- explicit Retry-After;
- max attempts;
- jitter boundedness;
- scheduled future job.

### Crash/failure tests

Cover F1-F7 and F9 with deterministic hooks/fakes.

F8 is documented as a channel-level ambiguous external side effect and tested in channel specs.

### Shutdown test

Start long handler, signal shutdown, ensure:

- no new claims;
- completed handler completes safely if within timeout;
- otherwise lease can recover.

### Secret redaction test

Use a sentinel secret and assert it is absent from:

- job persisted errors;
- logs;
- traces captured by test exporter.

## 45. Performance baseline

The queue must support the initial target:

```text
50 inbound events/sec burst
20 outbound jobs/sec
100 concurrent agents
```

without requiring Kafka.

Load testing proper is SPEC-180.

### NFR-EVT-004

Job claim queries must use partial indexes and bounded batches.

### NFR-EVT-005

Do not poll PostgreSQL with a zero-delay tight loop.

Use bounded sleep/backoff or PostgreSQL notification as an optimization, while preserving polling as correctness fallback.

## 46. Optional LISTEN/NOTIFY optimization

Allowed:

```text
INSERT job
-> pg_notify('jobs_available', ...)
```

Workers may wake early via `LISTEN`.

Correctness must not depend on notifications because PostgreSQL NOTIFY is not a durable queue.

## 47. Task decomposition

```text
030-01 jobs/outbox/inbound SQL migrations
030-02 job repository + claim query
030-03 lease token + completion guards
030-04 worker runtime + bounded concurrency
030-05 retry classifier + backoff/jitter
030-06 lease recovery
030-07 dead-letter/cancel/retry primitives
030-08 inbound StoreAndEnqueue transaction
030-09 OutboxWriter implementation
030-10 outbox dispatcher + handler job fan-out
030-11 handler registry
030-12 transactional event handler receipt wrapper
030-13 scheduling/run_at
030-14 graceful shutdown
030-15 metrics/tracing
030-16 failure-injection framework
030-17 concurrency tests
030-18 secret-redaction tests
030-19 package documentation/runbook
```

## 48. Codex implementation task

```text
Implement SPEC-030 Durable Events, Jobs and Transactional Outbox.

This is infrastructure for correctness, not a convenience queue.

Constraints:
- PostgreSQL is the durable queue and event store.
- Use FOR UPDATE SKIP LOCKED for multi-worker claiming.
- Every lease gets a unique lease token; stale workers cannot complete newer leases.
- Processing is at least once.
- Do not claim exactly-once semantics.
- Domain state + outbox event commit atomically.
- Outbox fan-out creates one durable job per registered handler.
- DB-only event handlers use an event-handler receipt in the same transaction as
  their side effects.
- Redis/PubSub may optimize wake-up/realtime later but cannot be required for
  correctness.
- Unknown handlers and permanent failures are observable, never silently ignored.
- Job/event payloads and errors must not persist secrets.

Required verification:
- duplicate inbound sequential and concurrent tests
- two-worker SKIP LOCKED test
- lease expiry and stale lease-token tests
- retry/permanent/dead tests
- scheduled job test
- outbox dispatcher concurrency test
- handler receipt crash-replay test
- F1-F7/F9 failure injection tests
- graceful shutdown test
- queue/outbox metrics
- secret redaction regression test
- go test -race

Before coding:
1. map implementation tasks to 030-01..030-19;
2. identify any transaction-boundary ambiguity;
3. do not replace PostgreSQL durability with Redis, Kafka or an external broker.

After coding:
1. run all tests;
2. report crash guarantees proven by tests;
3. list remaining ambiguous external-side-effect cases for channel specs.
```
