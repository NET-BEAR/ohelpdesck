# SPEC-030: durable dispatcher/jobs and worker leasing

Статус задачи: `in_design`. Дата: 2026-09-11. Владелец: оркестратор.

## Цель

Реализовать следующую проверяемую vertical после outbound lifecycle: PostgreSQL dispatcher превращает committed outbox event в idempotent durable jobs registered handlers; worker безопасно claim-ит/retries/recover-ит jobs через lease token и DB-only handler receipt. Реальный provider send не является частью этой задачи.

## Scope

Входит:

- migration 6 для `jobs`, statuses, indexes и `event_handler_receipts`;
- explicit code-owned handler registry and event-to-handler routes;
- bounded transactional outbox dispatcher with `FOR UPDATE SKIP LOCKED`;
- idempotent job creation `(event_id, handler)`;
- claim, completion, retry/dead, lease expiry recovery and stale-token fencing;
- bounded worker loop/shutdown, sanitized errors/minimal worker metrics;
- DB-only transactional receipt wrapper and remote PostgreSQL concurrency acceptance tests.

Не входит:

- real email/Telegram/VK/MAX provider adapters, credentials, network requests, external idempotency/reconciliation;
- inbound event persistence/webhook parsing;
- UI/admin dead-letter actions, SLA/analytics/periodic jobs, Redis durability;
- DB adapter follow-up from `DEC-OUT-01`.

## Артефакты и готовность

| Артефакт | Статус | Результат |
|---|---|---|
| [RND.md](RND.md) | ready_for_review | Checkout/spec evidence, boundaries and risks. |
| [BUSINESS_ANALYSIS.md](BUSINESS_ANALYSIS.md) | ready_for_review | As-is/to-be, business rules, vocabulary and traceability. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | draft | Schema, transaction/lease/receipt/runtime contract. |
| [ACCEPTANCE_TESTS.md](ACCEPTANCE_TESTS.md) | ready_for_review | Given/When/Then and remote-only evidence. |

## Decision gate

- **DG-030-01:** handling an outbox event with no registered durable route. Recommended A: retain it undispatched and observable until a route exists. Architecture confirmation is required before production code, because it defines whether future handler registration can consume historical facts.

## Delivery sequence

1. Architect resolves DG-030-01 and marks architecture ready.
2. Developer creates remote RED integration tests for AT-JOB-001..018 and additive migration 6.
3. Developer implements repository/registry/dispatcher, then worker/retry/receipt in minimal increments; all external calls stay absent.
4. CI validates branch; manual workflow dispatch deploys verified SHA to remote dev.
5. Remote migration, targeted/full `-race`, integration, coverage, `vet`, `gofmt` and runtime checks run on dev.
6. Independent reviewer checks locking, atomicity, fencing, error redaction and scope.
7. QA repeats remote acceptance/regression and records evidence; any defect returns to developer then review/QA.

## Completion criteria

Task becomes `completed` only after DG-030-01 is decided, remote evidence passes, independent review and QA approve, and documentation/plan results are committed. Functional success must say **at least once**, never exactly-once external delivery.

## Risks

- Incorrect no-route handling can silently discard future outbox work.
- A missing lease-token predicate permits stale worker corruption.
- A receipt outside handler transaction permits duplicate DB effects.
- Worker concurrency/polling without bounds can exhaust PostgreSQL.
- Provider delivery remains intentionally unproven until the next adapter slice.

## Решение DG-030-01 — 2026-09-11

Пользователь подтвердил продолжение с рекомендованным вариантом A: outbox event
без зарегистрированного handler не получает job и не помечается обработанным.
Он остаётся наблюдаемым durable фактом до явной регистрации route; это сохраняет
возможность безопасного future fan-out.
