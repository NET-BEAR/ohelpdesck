# QA и материалы ПСИ — SPEC-000

Дата: 2026-09-10. Статус: in_qa. Приёмка пока не завершена.

## Scope и окружение

Независимая роль QA. Ownership: только этот документ; production-код, тесты и PLANS.md не изменялись. Источники: spec/000-foundation.md, ACCEPTANCE_TESTS.md, REVIEW_INFRA.md, REVIEW_REPORT.md, IMPLEMENTATION_BACKEND.md, Compose, Makefile, CI workflow, delivery и integration tests. Запрошенный docs/agents/roles/qa-engineer.md отсутствует в checkout. Memory MCP, внешние трекеры и secrets не читались. Compose разрешено самостоятельно потреблять env-file, без печати содержимого/config.

Локальная ОС Darwin arm64; Docker client/server 29.7.2; Compose v5.5.0; delivery interpreter python:3.12.12-slim. Начальный проверенный HEAD: 12595624b311dcd89c3b91f91cd58297ec5d6b1e; рабочее дерево может содержать последующие исправления, runtime snapshot будет указан отдельно.

## Свежие независимые проверки

| Evidence | Команда / сценарий | Exit | Результат |
|---|---|---:|---|
| Q01 | `make test-delivery` | 0 | bash syntax, 8 behavioral tests PASS за 1.871 s: replay, first failure, upgrade rollback, invalid archive retry, command rejection, traversal rejection, same SHA content mismatch, trusted scripts/manifest |
| Q02 | `python3 deploy/check_coverage.py <temporary-profile>`: 5 покрытых и 5 непокрытых statements/lines | 1 | 50.00% / 50.00%, Required coverage is at least 80% |
| Q03 | Та же команда, профиль только `mode: set` | 1 | Coverage report is empty |
| Q04 | Та же команда, 10/10 statements/lines | 0 | 100.00% / 100.00% |

Q02–Q04 выполнялись Python harness с tempfile.NamedTemporaryFile, subprocess.run и assert точного returncode; aggregate exit 0. Синтетические profiles удалены автоматически. Это отрицательный контроль gate, не покрытие runtime. Delivery tests используют fake Docker/curl и временный deployment root; они доказывают control flow, но не реальный rollback контейнеров/SSH/данных.

## Матрица SPEC-000 для ПСИ

PASS требует свежего evidence; pending не является успешной приёмкой.

| Критерий | Метод / ожидаемый результат | Статус |
|---|---|---|
| AC-FND-001 / F01 | Clean bootstrap + dev, healthy api/worker/web/dependencies | pending |
| AC-FND-002 / F02 | Реальный API live HTTP 200 | pending final runtime |
| AC-FND-003 / F02 | ready HTTP 200, именованные postgres/redis/object_storage | pending final runtime |
| AC-FND-004 / F03 | Stop PG: live200/ready503, recovery ready200 | pending final runtime |
| AC-FND-005 / F04 | Реальные api и worker binaries получают SIGTERM, exit0 раньше grace period | pending |
| AC-FND-006 / F05 | Empty DB migrations down/status/up/up/status | pending integration |
| AC-FND-007 / F01 | make test / verify exit0 без integration skip | pending |
| AC-FND-008 / F06 | Валидный OpenAPI PASS, заведомо невалидный FAIL | pending |
| AC-FND-009 / F07 | make generate явно no-op, CI diff check api/openapi.yaml | static verified, generation not_applicable |
| AC-FND-010 / F08 | Redaction включая secret ancestor groups, telemetry response/query secrets | pending re-review + retest |
| AC-FND-011 / F09 | Frontend lockfile/typecheck/lint/unit/build; shared client | pending final verify; root сообщает visual smoke |
| AC-FND-012 / F05 | Каталог public содержит только schema_migrations | pending runtime catalog |
| F10 / SEC | Non-root api/worker/web, loopback ports; CORS/body/timeouts/config tests | pending runtime + suite |
| F11 | PG connect/tx rollback, Redis/S3 integration | pending integration |
| F12 | HTTP/DB/outbound telemetry, exporter failure security regression | pending re-review + retest |
| D01 | PR job verify; deploy исключён условием event, все проверки в make verify | static PASS; GitHub run pending |
| D02 | Deploy needs verify; git archive HEAD, command deploy GITHUB_SHA, health | static PASS; real delivery pending |
| D03 | SSH -F /dev/null, IdentitiesOnly, BatchMode, StrictHostKeyChecking, pinned known_hosts | static PASS; SSH negative control pending |
| D04 | First failure app-only stop; upgrade previous SHA/no DB down | Q01 PASS sandbox; runtime rollback pending |
| D05 | project ohelpdesck-dev; dependencies без host ports, app loopback | static PASS; host runtime pending |

CI: bootstrap → make verify → make dev → deploy/smoke.sh; teardown always ограничен ephemeral runner. make verify включает Go tests/integration/coverage gate/vet/race/fmt, frontend checks/build/audit, OpenAPI negative control, delivery tests, govulncheck и generate. Введён механический абсолютный порог >=80% statements и executable-block lines: INFRA-F03 частично закрыт; baseline comparison для будущих PR отсутствует. Проект foundation новый, текущий результат станет baseline; вычисление executable-block ranges не тождественно точному AST diff coverage.

## Дефекты и ретест

REVIEW_REPORT.md R1–R4 уже переданы разработчику. QA не дублирует баг-репорты: R1 raw OTLP errors, R2 sensitive outbound trace, R3 secret slog ancestor groups, R4 readiness Redis deadline. До подтверждения повторного независимого review runtime suite не запускалась. Новых дефектов в Q01–Q04 не выявлено.

## Протокол ПСИ и оставшиеся ограничения

Для окончательной приёмки: зафиксировать итоговый SHA и PASS повторного review; полную verify command/exit/coverage; финальный runtime image; health/failure/recovery, реальные SIGTERM, DB catalog, runtime UID и port binds; точный GitHub verify/deploy run и доставленный SHA. Сценарии выполняются на техническом foundation без business-domain таблиц/операций. Реальные remote операции координирует оркестратор во избежание коллизии.

status: in_qa  
artifacts: QA.md  
evidence: Q01–Q04; CI/AC static matrix  
risks: незавершённые runtime/re-review/CI/remote gates; sandbox не доказывает production behavior  
recommended_next_role: Ревьювер для исправлений R1–R4, затем продолжение QA
