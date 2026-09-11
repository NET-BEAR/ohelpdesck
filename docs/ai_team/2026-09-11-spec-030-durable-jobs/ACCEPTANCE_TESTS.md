# Acceptance tests: SPEC-030 durable dispatcher/jobs

Дата: 2026-09-11. Статус: `ready_for_review`. Все execution tests выполняются только на согласованном remote dev-контуре.

| ID | Given | When | Then |
|---|---|---|---|
| AT-JOB-001 | Committed undispatched outbox event и один registered durable handler | Dispatcher executes a batch | В той же transaction создана одна `domain_event` job с `event_id`/handler и только затем set `dispatched_at`. |
| AT-JOB-002 | Один event, два registered handlers | Dispatcher dispatches it | Созданы ровно две jobs, по одной на handler; event dispatched после обеих inserts. |
| AT-JOB-003 | Два dispatcher concurrently и один undispatched event | Both dispatch batches race | `(event_id,handler)` job существует ровно один раз; event dispatched, без partial duplicate fan-out. |
| AT-JOB-004 | Dispatcher failure injected after first job insert before commit | Batch runs then retries | First transaction has neither partial job set nor `dispatched_at`; retry creates canonical full fan-out once. |
| AT-JOB-005 | Pending job due now | Two workers claim concurrently | Только один получает `running` job с nonempty worker identity/token/expiry and attempts +1; другой gets no job. |
| AT-JOB-006 | Pending job with future `run_at`, cancelled job и completed/dead job | Worker claims | Ни одна из этих jobs не leased. |
| AT-JOB-007 | Worker A owns running job; lease expires; recovery then worker B claims | A attempts complete/reschedule/extend with old token | State remains B-owned; zero-row result is `stale_job_lease`, not success. |
| AT-JOB-008 | Expired running job | Recovery runs concurrently with claim/recovery | Job returns to pending once, old lease cleared, then is claimable with fresh token. |
| AT-JOB-009 | Running job handler returns transient typed error below max attempts | Worker handles it | Attempts remain recorded; lease cleared; status pending and future `run_at` respects bounded exponential delay+jitter. |
| AT-JOB-010 | Running job returns permanent error, or transient reaches max attempts | Worker handles it | Job is dead, never immediately re-claimed, retains sanitized code/message only. |
| AT-JOB-011 | DB-only handler has no receipt | It commits DB effect | Same transaction creates receipt and effect; fenced completion follows. |
| AT-JOB-012 | Receipt/effect committed but completion failure is injected | Job is recovered and invoked again | Handler observes receipt and makes no duplicate DB effect; eventual completion is fenced. |
| AT-JOB-013 | Job references unregistered/unknown handler | Worker resolves it | It becomes observable permanent configuration failure/dead, never silent success. |
| AT-JOB-014 | Sentinel secret appears in handler error or test input | Job/error/log/metric persistence is inspected | Sentinel, full payload/message text and credentials are absent; only sanitized stable fields persist. |
| AT-JOB-015 | Core mutation rolls back or commits while worker is down | Dispatcher/worker state is inspected | Rollback leaves no new outbox/job; committed event remains durable and later fan-outs; dispatcher does not weaken existing atomic outbox contract. |
| AT-JOB-016 | Outbox event has no registered route (provisional DG-030-01=A) | Dispatcher scans event | No job and no `dispatched_at`; backlog is observable and later explicit route can dispatch it. |
| AT-JOB-017 | Worker receives shutdown after claim while handler blocks beyond grace | Shutdown deadline passes and recovery runs | No new claims occur; unfinished job is not marked complete; expiry makes it recoverable. |
| AT-JOB-018 | Multiple ready priorities and bounded batch size | Dispatcher/worker polls | Lower numeric priority is claimed first, work is bounded, and no zero-delay busy loop occurs. |

## RED evidence expected before implementation

Before production code, remote integration suite must fail because migration 6/tables and dispatch/lease APIs do not yet exist. Tests must exercise PostgreSQL concurrency directly, including `SKIP LOCKED`; unit-only mocks do not prove lease/fencing correctness.

## Required remote validation after GREEN

```text
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm migrate up
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go test -count=1 -race ./tests/integration
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go vet ./...
```

Additionally inspect migration version 6, worker readiness/runtime, `gofmt`, CI/OpenAPI where changed, and function coverage for newly changed production code according to project policy.
