# SPEC-030: durable dispatcher/jobs and worker leasing

Статус задачи: `rework`. Дата: 2026-09-11. Владелец: оркестратор.

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
| [RND.md](RND.md) | approved | Checkout/spec evidence, boundaries and risks. |
| [BUSINESS_ANALYSIS.md](BUSINESS_ANALYSIS.md) | approved | As-is/to-be, business rules, vocabulary and traceability. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | approved | Schema, transaction/lease/receipt/runtime contract. |
| [ACCEPTANCE_TESTS.md](ACCEPTANCE_TESTS.md) | approved | Given/When/Then and remote-only evidence. |
| Implementation | rework | P1 review finding: exhausted-attempt lease recovery must become `dead`; Claim must never violate `attempts <= max_attempts`. |
| Remote validation | passed | CI/dev delivery `34635720836`; deployed SHA `a4fbea2`; migration 9, targeted jobs race and full remote suite passed. |
| Independent review | rework_required | P1 found in exhausted-attempt lease recovery; review repeats after fix. |
| QA | pending | Starts after review approval. |

## Decision gate

- **DG-030-01:** handling an outbox event with no registered durable route. Recommended A: retain it undispatched and observable until a route exists. Architecture confirmation is required before production code, because it defines whether future handler registration can consume historical facts.

## Delivery sequence

1. Architect resolves DG-030-01 and marks architecture ready.
2. Developer creates remote RED integration tests for AT-JOB-001..018 and additive migration 6.
3. Developer implements repository/registry/dispatcher, then worker/retry/receipt in minimal increments; all external calls stay absent.
4. CI validates branch; manual workflow dispatch deploys verified SHA to remote dev.
5. Remote migration, targeted/full `-race`, integration, coverage, `vet`, `gofmt` and runtime checks run on dev.
6. Independent reviewer checks locking, atomicity, fencing, error redaction, delivery configuration and scope.
7. QA repeats remote acceptance/regression and records evidence; any defect returns to developer then review/QA.

## Выполненное evidence — 2026-09-11

- CI/dev delivery [34635720836](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34635720836) завершён успешно: `verify` и `deploy-dev`.
- На dev-контуре подтверждён SHA `a4fbea218bb1bcfae4857f4dca54d6b9d3e87077` и migration version `9`.
- Удалённый `TestJobs` с `-race`, полный integration suite с `-race`, full Go coverage (**83,8% statements**), `go vet` и проверка `gofmt` завершились успешно.
- Предыдущая неуспешная доставка была вызвана устаревшим статическим server compose-манифестом без обязательной `CHANNEL_CREDENTIALS_AES256_KEY`; манифест обновлён операторским путём без чтения или вывода runtime secrets, после чего повторная доставка прошла.

## Review finding P1 — 2026-09-11

- **Причина:** `RecoverExpired` возвращал в `pending` job с `attempts == max_attempts`; следующий `Claim` увеличивал счётчик, нарушая DB constraint и оставляя poison-row во главе очереди.
- **Минимальная точка возврата:** implementation durable jobs. Разработчик исправляет recovery/claim и добавляет remote-ready regression для падения worker на последней попытке. Затем повторяются remote validation, независимый review и QA.

## Последующее улучшение delivery boundary

Статический compose-манифест намеренно не заменяется архивом из CI: это граница доверия deployment credential. Для будущих изменений его контракта требуется явное операторское обновление с резервной копией и post-update delivery verification. Эта операция зафиксирована как follow-up документации/инфраструктуры, а не скрыта в CI upload path.


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

## Финальное завершение — 2026-09-12

Статус задачи: `completed`.

- Финальный SHA: `de9f65421699c2cddbda3da94db2b1b1e6c73be2`.
- CI/CD [34681058254](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34681058254): `verify`, build/runtime smoke и `deploy-dev` завершились успешно.
- Удалённый dev подтверждён на этом SHA: API, worker, PostgreSQL, Redis, MinIO и web работают; schema migration `9`, readiness `200`, `queue_metrics_up=1`.
- Remote QA в изолированной PostgreSQL DB подтвердила последовательность AT017 → AT004 → combined `-race`; `go vet` и `gofmt` прошли. AT017 доказывает реальный SIGTERM, bounded runtime return, supervisor hard-stop и recovery истёкшего lease.
- Независимый review одобрил cumulative runtime changes и заключительный test-only patch. JOB-R1..JOB-R5 закрыты.
- Coverage final CI: 83.77% statements и 80.20% executable block lines; абсолютный порог 80% пройден. Историческое значение 84.0% получено иным harness/SHA и не является сопоставимым baseline; это зафиксировано как follow-up, а не скрыто.

Follow-up: отдельные execution scenarios natural process exit без supervisor SIGKILL и in-flight API DB operation во время shutdown drain не входят в доказанный acceptance scope и требуют самостоятельной задачи при изменении shutdown policy.
