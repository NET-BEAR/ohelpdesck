# QA — outbound reply: idempotency и lifecycle

- Проверяемая ревизия: `43264b6903522df323611aa32c443686e27813f8`
- Дата: 2026-09-11
- Проверяющий: независимый QA
- Вердикт: **REWORK: функциональные acceptance-сценарии прошли, но не подтверждено требование проекта о покрытии изменённых production-строк не ниже 80%.**

## Scope и ограничения

Проверка проведена только на согласованном удалённом dev-контуре
`root@vm742476.vps.masterhost.tech`. QA не менял production-код, миграции,
тесты, `PLANS.md`, `.gitignore` или Chatwoot artifacts. Запрошенный файл
`docs/agents/roles/qa-engineer.md` отсутствует в checkout; вместо него
использованы `AGENTS.md`, task artifacts, SPEC-020, реализация, integration
suite и [REVIEW_REPORT.md](REVIEW_REPORT.md). Memory MCP, `.env`, runtime
secrets и их значения не читались.

## Environment

- Активный release: `/opt/ohelpdesck-dev/releases/43264b6903522df323611aa32c443686e27813f8`.
- Compose запускается с существующим закрытым env-file без чтения его содержимого.
- `migrate up` завершился с кодом `0`: `migration version: 5`.
- API, web, PostgreSQL и Redis были healthy; worker работал. Runtime smoke
  (`/health/live` на API, `/health/ready` и `/health` на web) завершился с кодом `0`.

## Матрица acceptance и evidence

| ID | Сценарий | Evidence на remote dev | Результат |
|---|---|---|---|
| QA-OUT-01 | Один actor/key/equivalent request создаёт один queued Message/event; другой request даёт conflict | `TestQueueOutboundIdempotencyAndMembership`, targeted race run | PASS |
| QA-OUT-02 | Два конкурентных обращения с одним actor/key создают единственный Message/key | `TestQueueOutboundSameKeyRaceCreatesOneMessage` под `-race` | PASS |
| QA-OUT-03 | Queue требует session, CSRF, permission и `can_reply`; assignee не ограничивает участника channel | `TestQueueOutboundHTTPRequiresCSRFAndReturnsCanonicalRetry`, `TestQueueOutboundHTTPValidationAndAuthorizationFailures`, `TestQueueOutboundIdempotencyAndMembership` | PASS |
| QA-OUT-04 | `queued → sent` закрывает waiting, сохраняет начало episode и назначает first response DB-временем; поздний sent-failure возвращает только покрытый episode | `TestOutboundSentFailedLifecycleRestoresOnlyCoveredEpisode`; PostgreSQL mutation time применяется в реализации через `clock_timestamp()` | PASS |
| QA-OUT-05 | `queued → failed` не очищает waiting; correction не перезаписывает новый inbound episode | `TestOutboundValidationFailureAndQueuedFailurePreserveWaiting`, `TestOutboundSentFailedLifecycleRestoresOnlyCoveredEpisode` | PASS |
| QA-OUT-06 | Неверный transition/not found/blank failure code и outbox failure не дают частичной записи | `TestOutboundRejectsInvalidTransitionAndRollsBackOutboxFailure`, `TestOutboundValidationFailureAndQueuedFailurePreserveWaiting` | PASS |
| QA-OUT-07 | HTTP/OpenAPI описывают 200/201, 400/401/403/404/409 и Idempotency-Key | HTTP integration tests; inspection `api/openapi.yaml`; successful CI `34566347311` и delivery `34567101494` | PASS |
| QA-OUT-08 | Регрессия, race, format и vet | См. команды ниже | PASS |
| QA-OUT-09 | Изменённые production-строки имеют не менее 80% coverage | `go tool cover -func`: `QueueOutbound` 78.3%, `transition` 72.2%; точный changed-line отчёт отсутствует | **FAIL** |

## Команды и результаты

Все команды исполнялись на dev-host в `/opt/ohelpdesck-dev/current` через
`docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml`,
не раскрывая содержимое env-file.

- `migrate up` — exit `0`, `migration version: 5`.
- `go test -count=1 -race ./tests/integration -run 'TestQueueOutbound|TestOutbound'` — exit `0`, `ok`, 3.259 s.
- `go test -count=1 -race ./tests/integration` — exit `0`, `ok`, 12.254 s.
- `go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...` — exit `0`.
- `go tool cover -func=coverage.out` — total 83.7% statements; new functions: `QueueOutbound` 78.3%, `transition` 72.2%.
- `python3 deploy/check_coverage.py coverage.out` — exit `0`: 83.68% Go statements, 80.32% executable block lines (1400/1743).
- `go vet ./...` — exit `0`.
- `test -z "$(gofmt -l cmd internal tests)"` — exit `0`.
- Runtime smoke: API live, web ready and web root — exit `0`.

## Дефект / причина возврата

**QA-OUT-BUG-001 — недостаточно evidence покрытия изменённых production-ветвей.**

`AGENTS.md` задаёт обязательное условие: изменённые строки production-кода
должны быть покрыты не менее чем на 80%. Общий quality gate проходит, но
единственный доступный отчёт по новым функциям показывает 78.3% для
`QueueOutbound` и 72.2% для `transition`. Он не доказывает соблюдение
требования для изменённых строк; наоборот, показывает непокрытые ветви.

**Минимальное исправление:** добавить осмысленные integration tests для
непокрытых error-ветвей queue/lifecycle (в частности ошибки lookup/append,
canonical lookup и first-response preservation), получить changed-line либо
эквивалентный проверяемый coverage evidence не ниже 80%, затем повторить
remote targeted + full suite, coverage, vet и gofmt. Production-код менять для
этого дефекта не требуется, если тесты подтверждают существующий контракт.

## Материалы ПСИ

Для ПСИ подготовлены acceptance matrix QA-OUT-01…09, delivered SHA, migration
version, remote command evidence и runtime health. Принимать функциональный
контракт до устранения QA-OUT-BUG-001 нельзя: обязательный project quality gate
для изменённых строк не имеет достаточного подтверждения.

status: rework
artifacts: `docs/ai_team/2026-09-11-spec-020-outbound-lifecycle/QA.md`
evidence: release `43264b6903522df323611aa32c443686e27813f8`; remote migration version 5; targeted and full race PASS; coverage 83.68% statements / 80.32% executable lines; CI `34566347311`, delivery `34567101494`
risks: QA-OUT-BUG-001; provider dispatch/retry/delivered/read remains intentionally out of scope.
recommended_next_role: Разработчик для coverage rework, затем независимый Ревьювер и QA retest.
