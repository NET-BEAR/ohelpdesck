# Support Platform `/spec`

Status: active  
Purpose: source of truth for agent-first implementation  
Reference product: Chatwoot Community, used as a product/domain reference rather than as a codebase to port.

## 1. Governing principle

Implementation follows this lifecycle:

```text
requirement
  -> spec
  -> contracts
  -> tests
  -> implementation
  -> verification
  -> independent review
  -> complete
```

A production behavior is not considered defined unless it is traceable to a spec requirement or an accepted ADR.

## 2. Specification catalog

| ID | File | Purpose | Priority | Depends on |
|---|---|---|---|---|
| SPEC-000 | `000-foundation.md` | Repository, runtime, database, CI, configuration, API conventions | P0 | none |
| SPEC-010 | `010-auth-users-rbac.md` | Authentication, users, roles and authorization | P0 | 000 |
| SPEC-020 | `020-core-domain.md` | Channel-agnostic Contact, Identity, Conversation and Message domain | P0 | 000, 010 |
| SPEC-030 | `030-events-jobs-outbox.md` | Durable inbound events, jobs, retries and transactional outbox | P0 | 000, 020 |

Next planned specs:

```text
040 attachments
050 Telegram
060 VK
070 MAX
080 Email
090 unified inbox
100 queues and routing
110 response templates
120 workflow engine
130 customer context
140 SLA
150 analytics
160 AI copilot
170 security/audit/observability hardening
180 performance/resilience
190 production readiness
```

## 3. Status values

Every specification begins with one of:

```text
draft
ready
in_progress
verification
done
blocked
```

Codex may implement only `ready` or `in_progress` specifications.

## 4. Priority values

```text
P0 = required for MVP or required foundation
P1 = required for production v1
P2 = post-v1 enhancement
```

## 5. Requirement identifiers

Every normative requirement must have a stable ID:

```text
FR-xxx   functional requirement
NFR-xxx  non-functional requirement
SEC-xxx  security requirement
OBS-xxx  observability requirement
DATA-xxx data/integrity requirement
AC-xxx   acceptance criterion
```

IDs are never reused after deletion. Deprecated requirements remain in git history.

## 6. Repository target

```text
/
├── AGENTS.md
├── README.md
├── Makefile
├── api/
│   └── openapi.yaml
├── cmd/
│   ├── api/
│   └── worker/
├── internal/
│   ├── auth/
│   ├── channels/
│   ├── contacts/
│   ├── conversations/
│   ├── messages/
│   ├── events/
│   ├── jobs/
│   └── platform/
├── db/
│   ├── migrations/
│   └── queries/
├── web/
├── tests/
│   ├── fixtures/
│   ├── contract/
│   ├── integration/
│   └── e2e/
├── docs/
│   ├── architecture/
│   └── adr/
├── deploy/
└── spec/
```

## 7. Architecture constraints

### AR-001 Modular monolith

The system is a modular monolith. No new network service may be introduced without an ADR that demonstrates a concrete scaling, isolation or regulatory requirement.

### AR-002 PostgreSQL is the source of truth

Messages, conversations, durable jobs, inbound events and domain events must survive Redis loss and process restarts.

### AR-003 Provider isolation

Telegram, VK, MAX and Email provider types may exist only inside their channel adapters.

Core modules operate on normalized contracts.

Forbidden in core:

```go
if conversation.ChannelType == "telegram" {
    // provider-specific behavior
}
```

### AR-004 Event-driven side effects

A business state change and the domain event describing it are committed atomically using a transactional outbox.

### AR-005 At-least-once processing

Workers must assume duplicate delivery. Correctness comes from idempotency, unique constraints and transactional handlers, not from an exactly-once assumption.

### AR-006 Optional dependencies cannot break messaging

AI, analytics, customer enrichment and realtime delivery are secondary capabilities. Their unavailability must not prevent receiving, storing or sending support messages unless a provider itself is unavailable.

### AR-007 Explicit transaction boundaries

Business transactions are owned by application services. Repository methods do not silently create nested business transactions.

### AR-008 No hidden ORM behavior

Prefer explicit SQL through `pgx`/`sqlc` or a thin explicit repository layer. Do not introduce ActiveRecord-like callback behavior.

### AR-009 OpenAPI is the HTTP contract

`api/openapi.yaml` is the source of truth for externally consumed HTTP contracts.

### AR-010 Time

Persist timestamps in UTC using timezone-aware PostgreSQL types (`timestamptz`). Business calendars and display timezones are separate concepts.

## 8. Definition of Ready

A spec is ready only if:

- goal and non-goals are explicit;
- dependencies are available;
- domain terms are defined;
- state transitions are defined;
- API contracts are defined where applicable;
- failure behavior is defined;
- acceptance criteria are testable;
- required tests are listed.

## 9. Definition of Done

A spec is done only when:

- implementation is complete;
- unit tests pass;
- PostgreSQL integration tests pass;
- contract tests pass where applicable;
- race/concurrency tests pass where applicable;
- OpenAPI is updated;
- migrations are tested from an empty database;
- forward migration is tested against the previous schema;
- logs and metrics exist for critical paths;
- secret/PII handling is reviewed;
- an independent reviewer has checked the implementation against the spec.

## 10. Codex lifecycle

For each spec:

```text
REFINE
  -> TASKS
  -> IMPLEMENT
  -> VERIFY
  -> REVIEW
  -> COMPLETE
```

### REFINE

An architecture agent checks consistency, coupling, transaction boundaries, security risks and missing edge cases. It does not write production code.

### TASKS

The spec is decomposed into small vertical tasks. A good task normally changes one bounded area and has explicit tests.

### IMPLEMENT

A coding agent receives:

- the spec;
- its task;
- relevant ADRs;
- relevant interfaces;
- architecture rules from `AGENTS.md`.

### VERIFY

Run at minimum:

```bash
go test ./...
go vet ./...
go test -race ./...
```

plus frontend/integration checks as applicable.

### REVIEW

The reviewer must not be the same agent that produced the implementation.

Review priorities:

1. data loss;
2. duplicate message creation;
3. wrong recipient/conversation;
4. authorization bypass;
5. broken transaction boundaries;
6. unsafe retries;
7. race conditions;
8. provider leakage into core;
9. unobservable failure modes;
10. unnecessary architectural complexity.

## 11. Change policy

When observed production behavior conflicts with the spec:

```text
incident/bug
  -> add regression fixture/test
  -> decide intended behavior
  -> update spec if necessary
  -> implement correction
```

Do not silently change implementation and document it later.

## 12. Chatwoot reference notes

The project deliberately borrows several proven product concepts from Chatwoot Community:

- `Inbox/Channel -> Contact -> Conversation -> Message`;
- separate external identity per channel;
- open/resolved/pending/snoozed lifecycle;
- assignment as a separate concern;
- message direction and delivery status;
- event-driven first-response/reply metrics;
- workflow model based on event, conditions and actions;
- canned responses/templates;
- AI summary/reply assistance.

The implementation is intentionally smaller and is not required to preserve Chatwoot API, database or framework compatibility.
