# QA и материалы ПСИ — SPEC-000

Дата: 2026-09-10. Статус: in_qa. Локальная приёмка финального foundation PASS; remote CI/CD gates ещё не завершены.

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
| AC-FND-001 / F01 | Bootstrap/full dev final | PASS local make dev; clean hosted checkout pending CI |
| AC-FND-002 / F02 | Реальный API live HTTP 200 | PASS Q06 |
| AC-FND-003 / F02 | ready HTTP 200, postgres/redis/object_storage=ok | PASS Q06 |
| AC-FND-004 / F03 | Stop PG: live200/ready503, recovery ready200 | PASS Q06 |
| AC-FND-005 / F04 | Реальные api/worker SIGTERM, exit0 раньше grace period | PASS Q05 |
| AC-FND-006 / F05 | Empty DB migrations down/status/up/up/status | PASS final integration Q09 |
| AC-FND-007 / F01 | make verify exit0, integration без skip | PASS local Q09; clean CI pending |
| AC-FND-008 / F06 | Валидный OpenAPI PASS, невалидный FAIL | PASS Q09 |
| AC-FND-009 / F07 | make generate явно no-op, CI diff api/openapi.yaml | PASS Q09; generation not_applicable |
| AC-FND-010 / F08 | Secret groups и telemetry privacy regressions | PASS Q09 и независимый re-review R1–R5 |
| AC-FND-011 / F09 | Frontend typecheck/lint/unit/build; shared client | PASS Q09, 27 tests; visual smoke — evidence root |
| AC-FND-012 / F05 | Каталог содержит только schema_migrations | PASS Q07 |
| F10 / SEC | Non-root, loopback; CORS/body/timeouts/config | PASS Q08/Q09 |
| F11 | PG connect/tx rollback, Redis/S3 integration | PASS Q09 |
| F12 | Telemetry/exporter failure regression | PASS Q09 и re-review |
| D01 | PR verify; deploy исключён event condition | static PASS; GitHub run pending |
| D02 | Deploy needs verify, archive HEAD/deploy SHA, health | static PASS; remote pending |
| D03 | StrictHostKeyChecking/pinned known_hosts/isolated identity | static PASS; SSH negative control pending |
| D04 | First failure app-only stop; upgrade previous SHA/no DB down | Q01 PASS sandbox; remote rollback pending |
| D05 | project ohelpdesck-dev, dependencies без host ports | PASS local Q08; remote pending |

CI: bootstrap → make verify → make dev → deploy/smoke.sh; teardown always ограничен ephemeral runner. make verify включает Go tests/integration/coverage gate/vet/race/fmt, frontend checks/build/audit, OpenAPI negative control, delivery tests, govulncheck и generate. Введён механический абсолютный порог >=80% statements и executable-block lines: INFRA-F03 частично закрыт; baseline comparison для будущих PR отсутствует. Проект foundation новый, текущий результат станет baseline; вычисление executable-block ranges не тождественно точному AST diff coverage.

## Дефекты и ретест

REVIEW_REPORT.md R1–R4 уже переданы разработчику. QA не дублирует баг-репорты: R1 raw OTLP errors, R2 sensitive outbound trace, R3 secret slog ancestor groups, R4 readiness Redis deadline. Повторный REVIEW_REPORT.md одобрил R1–R5 на 0715a6e после targeted race count=3. После этого выполнены Q05–Q08. Полный suite Q09 повторно не дублировался. Новых дефектов в Q01–Q08 не выявлено.

## Протокол ПСИ и оставшиеся ограничения

Для окончательной приёмки: зафиксировать итоговый SHA и PASS повторного review; полную verify command/exit/coverage; финальный runtime image; health/failure/recovery, реальные SIGTERM, DB catalog, runtime UID и port binds; точный GitHub verify/deploy run и доставленный SHA. Сценарии выполняются на техническом foundation без business-domain таблиц/операций. Реальные remote операции координирует оркестратор во избежание коллизии.

status: in_qa  
artifacts: QA.md  
evidence: Q01–Q04; CI/AC static matrix  
risks: незавершённые runtime/re-review/CI/remote gates; sandbox не доказывает production behavior  
recommended_next_role: Оркестратор для GitHub CI/dev delivery, затем QA remote gates


## Финальный локальный ретест

Snapshot: `0715a6e102a48c4e06fbdbe4d833655f1046b0ac`. Независимый reviewer APPROVE; R1–R5 закрыты. `make dev` пересобрал финальные образы; `/tmp/ohelpdesck-dev-final.log` завершился healthy API/web/dependencies, затем начат QA. Локальный вердикт: **PASS**. Remote acceptance ещё pending.

| Evidence | Команда / точный метод | Exit | Результат |
|---|---|---:|---|
| Q05 | Для api и worker: `docker compose --env-file .env -f deploy/compose.yml run -d --no-deps SERVICE`; `docker exec CID curl --fail --silent --max-time 3 http://localhost:9090/metrics`; `docker stop --time 15 CID`; inspect только `.State`; `docker rm -f CID` | 0 | Реальные process shutdown: API 0.418 s, worker 7.717 s, оба ExitCode=0, OOMKilled=false. Python harness assert elapsed<15 и listener до сигнала; one-offs удалены |
| Q06 | `bash deploy/smoke.sh` | 0 | live200/ready200, web доступен, public /metrics=404; PG stop → live200/ready503; PG start → ready200 все именованные checks=ok |
| Q07 | `docker compose --env-file .env -f deploy/compose.yml exec -T postgres psql -U support -d support -Atc "SELECT schemaname || '.' || tablename FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY 1"` | 0 | Ровно `public.schema_migrations`; бизнес-таблиц нет |
| Q08 | Compose ps IDs; `docker inspect --format` только Config.User, HostConfig.PortBindings, ReadonlyRootfs, CapDrop; `docker exec CID id -u` | 0 | api/worker UID65532, web UID101; cap_drop ALL; api/worker readonly=true. Web readonly=false не нарушает требование nonroot. API18081/metrics19090/web18080 только 127.0.0.1, worker/PG/Redis/MinIO без host binds |
| Q09 | Оркестратор: `make verify`, `/tmp/ohelpdesck-verify-final.log`; QA прочитал выбранные строки результата | 0, сообщён оркестратором | Go tests/integration/vet/race/fmt, frontend typecheck/lint/build 27 tests PASS; OpenAPI valid + negative rejected; delivery8 PASS; govulncheck reachable0, npm vulnerabilities0; Go statements88.60%, executable block lines87.30% (385/441), frontend90.9% |

Q05–Q08 выполнены QA непосредственно. Q09 — свежий результат другого исполнителя, подтверждённый чтением log; не выдаётся за независимый повтор всего suite. SIGTERM проверяет реальные сигналы и штатный выход foundation lifecycle, но не обработку бизнес-jobs, которых ещё нет. PG outage выполнен после завершения verify в согласованном эксклюзивном окне и восстановлен; удалённый контур не затрагивался.

Следующий этап ПСИ: exact hosted CI run/commit, remote delivered SHA/readiness, remote rollback и SSH boundary evidence. До этого SPEC-000 clean-checkout/remote completion не утверждается.

## Локальный ретест после первого hosted CI — Q10–Q11

Оркестратор сообщил RED: CI run `34441479440` остановился из-за root-owned configuration mode0600; прямой `/health` возвращал 301 на внутренний порт8080. Исправления перед QA одобрены независимым infra reviewer. Проверен текущий Makefile: bootstrap использует `--user "$(id -u):$(id -g)"` и `test -r`; Nginx имеет exact location `/health`. Эти строки не изменялись QA.

- **Q10 PASS, exit0**: `docker run --rm --user 1001:1001 --tmpfs /qa:rw,uid=1001,gid=1001,mode=0700 -v "$PWD/deploy/bootstrap.py:/bootstrap.py:ro" -e ENV_FILE=/qa/generated-config python:3.12.12-slim sh -ec 'python /bootstrap.py; test -r /qa/generated-config; ...stat assertions...'`. Python assertions проверили `st_uid==1001`, `stat.S_IMODE(st_mode)==0o600`; результат `uid=1001 mode=0600 readable=true`. Содержимое файла не читалось и не печаталось; tmpfs удалён вместе с контейнером.
- **Q11 PASS, exit0**: Python `urllib.request` с `HTTPRedirectHandler.redirect_request → None` открыл `http://127.0.0.1:18080/health`, проверил status200, Content-Type text/html, отсутствие Location. Дополнительно `/health/ready` через web proxy вернул200. Таким образом результат не скрывает redirect follow.

Локальный вердикт для двух исправлений: **PASS**. Полный backend suite повторно не запускался. Исправление изоляции `/src/web/node_modules` для Go tool-container проверяется повторным CI; hosted CI success и удалённый rollout остаются pending.
