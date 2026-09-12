# Независимая QA — SPEC-030 durable jobs

Дата: 2026-09-12. Статус: **completed — QA PASS в проверенном SPEC-030/dev scope**. Финальный SHA `de9f65421699c2cddbda3da94db2b1b1e6c73be2`. История rework сохранена; актуальное решение приведено в финальном разделе.

## Scope и окружение

Источники: ACCEPTANCE_TESTS.md, финальный REVIEW_REPORT.md с approve JOB-R1–R4, committed исходники/tests из `/opt/ohelpdesck-dev/current`, actual dev runtime и GitHub metadata. Все execution tests выполнялись только на `root@vm742476.vps.masterhost.tech`, Docker tool image `golang:1.25.13-bookworm`, PostgreSQL dev Compose. SSH identity `~/.ssh/id_ed25519_masterhost`, BatchMode; контроль deployed SHA использовал StrictHostKeyChecking=yes. Локальные тесты не запускались. Production, тесты, PLANS.md, review и чужой Telegram diff QA не менял. Memory MCP и secrets не читались; Compose сам потреблял runtime.env без вывода содержимого.

`docs/agents/roles/qa-engineer.md` отсутствовал при предыдущей проверке этого checkout; отдельная ролевая инструкция не является evidence исполнения. Remote `rg` отсутствует, для ограниченного чтения исходников использован grep. Две discovery-команды обращались к несуществующим dispatcher.go/worker.go; фактическая реализация находится в jobs.go. Эти read-only ошибки не относятся к результату suite.

## Команды и свежие evidence

| ID | Проверка | Exit / evidence |
|---|---|---|
| JQ01 | `cat /opt/ohelpdesck-dev/current-sha`; `docker exec ohelpdesck-dev-postgres-1 psql -U support -d support -Atc "SELECT version FROM schema_migrations ORDER BY version"` | SHA совпадает; версии 1…9. Первичная составная discovery-команда также сообщила отсутствие rg; SHA/SQL результаты получены успешно |
| JQ02 | `gh run view 34648800796 --repo NET-BEAR/ohelpdesck --json headSha,conclusion,jobs` | exit0, exact SHA, verify+deploy-dev success, включая full verification, build/start, smoke, pinned SSH delivery |
| JQ03 | `go test -v -count=1 -race ./internal/jobs ./tests/integration -run "TestJob|TestRetryDelay"` внутри remote tool container | exit0: 3 selected unit tests + 19 jobs integration tests PASS; unit1.023s, integration1.921s |
| JQ04 | `go test -count=1 -race ./tests/integration` в том же container | exit0, integration18.745s |
| JQ05 | `go vet ./...`; `test -z "$(gofmt -l cmd internal tests)"` | обе exit0, общий remote harness exit0 |
| JQ06 | `curl --fail --silent --max-time 10 http://127.0.0.1:19090/metrics` с фильтром только required series | exit0: queue_metrics_up1, outbox_dispatch_lag_seconds0, outbox_undispatched_events0; job_queue_depth cancelled0/completed0/dead1/pending0/running0 |
| JQ07 | После cleanup: SQL max(version), count QA database; `curl --fail --silent --max-time 10 http://127.0.0.1:18081/health/ready` | exit0, live schema version9; QA DB count0; ready и все checks ok |

CI: https://github.com/NET-BEAR/ohelpdesck/actions/runs/34648800796 . Verify завершился 2026-09-11T21:23:52Z, deploy-dev21:25:53Z. Это фактический clean CI full verify/deploy evidence; новое точное число покрытия QA не вычислял. Старые 84.0% из review относятся к предыдущему SHA и здесь не переносятся на новый.

### Изоляция тестовых данных

Full integration содержит migration down. Поэтому QA согласовал с оркестратором отдельную временную DB `qa_spec030_20260912` на том же dev PostgreSQL. Live `support` не использовалась для test migrations. Создание: `docker exec ohelpdesck-dev-postgres-1 createdb -U support qa_spec030_20260912`. EXIT trap: `docker exec ohelpdesck-dev-postgres-1 dropdb -U support --force qa_spec030_20260912`.

В `/opt/ohelpdesck-dev/current` установлен `RELEASE_SHA=239057bc0f06297a876aa043eb88171b897e2ad1`; запуск:

```sh
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env \
  -f deploy/compose.yml run --rm --no-deps go sh -ec '
export DATABASE_URL="${DATABASE_URL%/support*}/qa_spec030_20260912?sslmode=disable"
go test -v -count=1 -race ./internal/jobs ./tests/integration -run "TestJob|TestRetryDelay"
go test -count=1 -race ./tests/integration
go vet ./...
test -z "$(gofmt -l cmd internal tests)"
'
```

DSN не выводился. Общий SSH harness `set -eu` завершился0; DB удалена trap, подтверждено JQ07. Redis/MinIO предоставлены dev dependency network; integration использует тестовые fixtures. Existing dead1 в live metrics не менялся; причина этой записи не исследовалась, её наличие само по себе не дефект.

## AT-JOB matrix

PASS означает подтверждённую проверяемую часть; LIMIT не выдаётся за полное исполнение исходного Given/When/Then.

| AT | Результат | Evidence / границы |
|---|---|---|
| 001 | PASS | DispatcherFanoutIdempotencyAndUnknownRetention; событие отмечено после jobs, transaction source reviewed |
| 002 | PASS | Два handlers → ровно2 jobs, повтор→0 |
| 003 | PASS | ConcurrentDispatcherCreatesCanonicalFanout: два dispatcher, одна canonical job |
| 004 | GAP-J04 | Нет fault injection после первой вставки fan-out до commit; обычный success и receipt rollback этот сценарий не заменяют |
| 005 | PASS с ограничением assertions | ClaimFencingRecoveryAndEligibility запускает две concurrent claims; один expected due job/attempt1; тест явно не считает оба nonnil результата, exclusivity дополнительно опирается на reviewed SKIP LOCKED |
| 006 | PASS основной eligibility | Future и completed не claim; dead проверен permanent/exhausted tests. Отдельного cancelled-row execution сценария не найдено |
| 007 | PASS | Fresh token после expiry; stale Complete/Reschedule/Extend отвергнуты ErrStaleJobLease в совокупности lease tests |
| 008 | GAP-J08 | Expiry/recovery/bounded batch и exhausted→dead проходят, но recovery выполняется последовательно; гонка recovery/claim/recovery не вызвана |
| 009 | PASS | Worker retry tests + unit bounded injectable jitter/provider hint/backoff cap |
| 010 | PASS | Permanent/capped/exhausted policies, fixed sanitized persistence, exhausted never reclaimed |
| 011 | PASS | Receipt/effect transaction и receipt rollback при handler error проверены |
| 012 | GAP-J12 | RunReceipt повторяется и dedup подтверждён; нет exact injected completion failure → expiry/recovery → повтор handler → eventual fenced complete |
| 013 | PASS | UnknownHandlerAndLifecycleValidation, unknown handler → dead |
| 014 | PASS проверенных persistence/metric границ | Unit sanitize + opaque Code/Message/Cause integration → fixed internal_error/job failed; metrics не включают event identity. Отдельный runtime log capture opaque credentials в jobs не выполнялся |
| 015 | PASS atomic core/outbox | Full integration core rollback/commit + durable fanout; реальный остановленный worker отдельно не инсценирован |
| 016 | PASS retention/observability | Unknown event остаётся undispatched без jobs; metrics test видит retained backlog; late route registration отдельно не исполнялся |
| 017 | GAP-J17 | Cancellation оставляет running job и Run прекращается; нет blocked handler beyond grace + реального SIGTERM + recovery after expiry |
| 018 | PASS bounded policy, ограниченный runtime evidence | Batch/interval validation и retry caps проверены; priority/recovery fixtures присутствуют. Отдельного наблюдения CPU/частоты idle loop не было |

Суммарное GREEN не отменяет указанные ограничения. Четыре главных acceptance gaps переданы оркестратору для разработчика; остальные частичные assertions явно отмечены и не считаются скрыто проверенными.

## Материалы ПСИ: точные недостающие сценарии

1. **GAP-J04 / AT-JOB-004**: event с двумя handlers, инъекция DB failure после первой job insert до commit. Проверить jobs=0 и dispatched_at=NULL после rollback; повтор без failure создаёт2 jobs один раз.
2. **GAP-J08 / AT-JOB-008**: один expired job, одновременно recovery/recovery/claim с barrier. Проверить отсутствие двойного владения/потери, fresh token и stale fence; повторить с race detector.
3. **GAP-J12 / AT-JOB-012**: DB effect+receipt commit, затем completion failure. Принудительная expiry, recovery/new claim, повтор handler; effect count1, receipt1, финальный completed с актуальным token.
4. **GAP-J17 / AT-JOB-017**: worker process владеет job, handler блокируется дольше shutdown grace. SIGTERM, нет новых claims и false completion; после expiry другой worker восстанавливает job. Process-based evidence должно отличаться от context cancellation unit/integration test.

Это пробелы доказательств, не новые подтверждённые production bugs. Исправленные JOB-R1–R4 прошли соответствующие targeted regressions; в проверенных сценариях новых дефектов не выявлено. После добавления сценариев нужен независимый review и remote QA ретест на новом SHA, без migration down в live DB.

## Передача

- status: **rework**, acceptance evidence неполная; execution suite GREEN.
- artifacts: этот QA.md.
- evidence: JQ01–JQ07, exact SHA/CI, isolated remote targeted+full integration race, vet/gofmt, actual metrics, cleanup/health.
- risks: GAP-J04/J08/J12/J17 и ограничения матрицы; внешняя provider delivery здесь не проверяется; live dead job не классифицирован; точное новое coverage число не измерено QA.
- recommended_next_role: **Разработчик → Ревьювер → QA-инженер**.

## Ретест новых acceptance scenarios — d35941a

2026-09-12. SHA `d35941aad2264e123ce18054fdeaa2cebbeac87d`, независимо подтверждён чтением deployed current-sha. `gh run view 34649755385 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` — exit0, exact SHA/success; run https://github.com/NET-BEAR/ohelpdesck/actions/runs/34649755385 .

**JQ08 PASS:** четыре новых tests выполнены QA непосредственно на remote в отдельной DB `qa_spec030_retest_20260912`:

```sh
go test -v -count=1 -race ./tests/integration \
  -run 'TestJobs(DispatcherRollback|ConcurrentRecovery|WorkerShutdown|ReceiptEffect)'
go test -count=1 -race ./internal/jobs ./tests/integration
```

Использован тот же Compose remote harness с `--no-deps`, exact RELEASE_SHA и подменой только DB path без вывода DSN. Первая команда exit0, все4 PASS,1.326s. Вторая exit0: jobs1.026s, full integration19.294s. Общий SSH harness exit0. DB создана через createdb и удалена EXIT trap dropdb --force.

**JQ09 PASS:** после завершения SQL подтвердил max live migration9, QA DB count0; ready200 все checks ok. Live database не подвергалась test migrations down.

### Изменение verdict по gaps

- **GAP-J04 частично закрыт:** новый PostgreSQL trigger отвергает вторую fan-out вставку; jobs0/dispatched_atNULL после rollback подтверждены. Но сам тест заканчивается на этих assertions: снятие failure и canonical retry отсутствует. Обычный fanout/repeat проходит отдельно, поэтому atomic rollback доказан, exact combined failure→retry часть AT004 остаётся ограничением.
- **GAP-J08 закрыт для AT008 recovery/claim race:** barrier одновременно запускает RecoverExpired/Claim, recovered1, budget≤2, fresh claim и completion. Второй concurrent recovery отдельным actor не добавлен; AT wording допускает recovery с claim.
- **GAP-J12 закрыт:** настоящий DB effect/receipt commit, Complete с неправильным token возвращает stale, expiry/recovery/new claim, повтор receipt не запускает effect, финальное completion и counts effect1/receipt1. Инъекция выполнена token mismatch, а не сетевым разрывом; проверяемое recovery/dedup свойство подтверждено.
- **GAP-J17 остаётся:** `TestJobsWorkerShutdownLeavesClaimForExpiryRecovery` использует `context.WithCancel` внутри текущего test process. Handler реагирует на `ctx.Done`, worker выходит в1s. Нет отдельного процесса/OS SIGTERM, handler не блокируется beyond grace, нет проверки отсутствия новых claims в grace window. Поэтому PASS этого теста подтверждает cooperative cancellation+expiry recovery, но не исходный AT017.

**Актуальный status: rework.** Новых production дефектов не воспроизведено,4 tests и relevant full race GREEN. Для полного acceptance остаются GAP-J17 и combined retry assertion AT004. REVIEW_REPORT на момент QA содержит approve production239057b; независимый reroute review новых tests ещё не был зафиксирован в прочитанном отчёте. Рекомендация: Разработчик завершает exact scenarios → независимый Ревьювер → QA remote retest. Предыдущая AT-матрица уточняется этим разделом, completed не заявляется.

## Финальный remote ретест f3729a5 — обнаружен failing regression

Дата: 2026-09-12. **Актуальный verdict: rework, FAIL relevant race suite.** SHA `f3729a5d053cb06a1f0562598d4beb0c7d0eeee0` подтверждён remote current-sha; независимый `gh run view 34673895183 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` exit0 показывает exact SHA/success (https://github.com/NET-BEAR/ohelpdesck/actions/runs/34673895183). Успешный CI не отменяет следующего свежего независимого failure.

**JQ10:** isolated remote DB `qa_spec030_final_20260912`; создана/удалена прежним createdb/EXIT trap dropdb. Tool image golang1.25.13; working tree `/opt/ohelpdesck-dev/current`; exact RELEASE_SHA; DATABASE_URL path заменён внутри container без вывода. `-p 1` исключает параллельное выполнение packages, использующих общую QA DB.

```sh
go test -v -p 1 -count=1 -race ./cmd/worker ./tests/integration \
  -run 'TestWorkerSIGTERMBoundedShutdownAndLeaseRecovery|TestJobsDispatcherRollbackLeavesOutboxUndispatched'
go test -p 1 -count=1 -race ./internal/jobs ./cmd/worker ./tests/integration
```

Первая команда **exit0**: SIGTERM test PASS0.24s (package1.266s); fanout rollback+retry PASS0.04s (package1.072s). Source подтверждает снятие failure trigger → jobs2/dispatched → repeat0, поэтому AT004 закрыт. SIGTERM test запускает subprocess через тот же `runWorker` signal path; handler игнорирует cancel5s, grace100ms; runtime-return marker проверяется до400ms, далее hard-stop и expiry/recovery. Это существенное улучшение AT017 evidence; успешный одиночный запуск не достаточен при следующем падении.

Вторая команда **exit1**:

```text
ok github.com/NET-BEAR/ohelpdesck/internal/jobs 1.021s
--- FAIL: TestWorkerSIGTERMBoundedShutdownAndLeaseRecovery (0.18s)
    main_test.go:176: SIGTERM job status=pending err=<nil>
FAIL github.com/NET-BEAR/ohelpdesck/cmd/worker 0.225s
ok github.com/NET-BEAR/ohelpdesck/tests/integration 16.275s
FAIL
```

Общий remote harness exit1. Следовавшие за suite coverage/vet/gofmt команды **не выполнялись** из-за `sh -ec`; новых значений покрытия/vet/gofmt на f3729a5 QA не заявляет. Предыдущие GREEN относятся к предыдущим SHA.

### QA-J17-FAIL — inconsistent post-SIGTERM persisted state

Приоритет: P1 для acceptance gate, production impact пока не установлен. Окружение и команды JQ10. Предусловия: свежая отдельная QA DB, последовательный targeted запуск, затем relevant full race suite на том же committed source. Ожидание теста: unfinished leased job остаётся running до искусственной expiry/recovery. Факт: full suite получил pending без SQL error. Отдельный targeted запуск непосредственно перед ним прошёл. Это подтверждённое падение, а не установленная первопричина; возможные причины в runtime/test isolation должны исследоваться разработчиком. Не считать результат доказательством потери/дублирования jobs без дополнительного расследования.

**JQ11 cleanup PASS, exit0:** QA DB count0; live `max(schema_migrations.version)=9`; ready200 со всеми checks ok. Live schema не подвергалась down; временные test fixtures удалены вместе с QA DB. Production-код QA не изменял.

Закрытие AT017 блокируется QA-J17-FAIL. Также на момент чтения REVIEW_REPORT последний reroute review был d35941a: изменения runtime/runWorker требуют нового независимого review. **status: rework; artifacts: QA.md; evidence: JQ10–JQ11; risks: failure above, непроверенные новые coverage/vet/gofmt; recommended_next_role: Разработчик → Ревьювер → remote QA.**

## Повтор QA-J17-FAIL на ec774a9

2026-09-12. **Status остаётся rework.** Exact deployed SHA `ec774a9b47c98bd0bc1dae2975b5d43b5b525da3`; независимый `gh run view 34676157333 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` exit0, exact SHA/success. CI: https://github.com/NET-BEAR/ohelpdesck/actions/runs/34676157333 .

**JQ12:** тот же remote harness и последовательность JQ10, только отдельная DB `qa_spec030_ec774a9` и текущий RELEASE_SHA. Targeted AT017 PASS0.23s, AT004 PASS0.04s, первая команда exit0. Следующий `go test -p 1 -count=1 -race ./internal/jobs ./cmd/worker ./tests/integration` снова exit1:

```text
ok github.com/NET-BEAR/ohelpdesck/internal/jobs 1.028s
--- FAIL: TestWorkerSIGTERMBoundedShutdownAndLeaseRecovery (0.10s)
    main_test.go:138: handler-ready job status=pending err=<nil>
FAIL github.com/NET-BEAR/ohelpdesck/cmd/worker 0.147s
ok github.com/NET-BEAR/ohelpdesck/tests/integration 15.750s
FAIL
```

Теперь failure обнаружен новой pre-SIGTERM assertion, до отправки сигнала. Это уточняет QA-J17-FAIL: marker handler-started не соответствует ожидаемому running состоянию job текущего event. Причина пока не установлена; developer должен проверить оставшиеся fixtures после первого targeted run и привязку marker к конкретному event. Ни потеря jobs, ни дефект самого OS signal handling этим падением не доказаны.

Общий harness exit1. Coverage/vet/gofmt после suite снова не исполнялись из-за fail-fast. **JQ13 cleanup PASS:** отдельная QA DB удалена/count0, live migration9 и ready200/все checks ok. Production и live migration не менялись. Новое code review ec774a9 на момент начала QA не было подтверждено: прочитанный REVIEW_REPORT заканчивается JOB-R5 needs_changes для f3729a5.

Передача: **rework**, QA-J17-FAIL не закрыт; artifacts QA.md; evidence JQ12–JQ13; next Разработчик → независимый Ревьювер → remote QA той же последовательности targeted→combined suite в одной изолированной DB.

## Финальный успешный ретест 76dced6

2026-09-12. Точный SHA `76dced61cd091284bcc92d6635257608b949dca4`; remote current-sha и `gh run view 34678885611 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` независимо подтверждают exact SHA/success. CI: https://github.com/NET-BEAR/ohelpdesck/actions/runs/34678885611 .

**JQ14 PASS, общий SSH harness exit0.** Одна свежая isolated DB `qa_spec030_76dced6`, тот же remote Compose harness, DATABASE_URL path заменяется без вывода; packages последовательно через `-p1`:

```sh
go test -v -count=1 -race ./cmd/worker -run TestWorkerSIGTERMBoundedShutdownAndLeaseRecovery
go test -v -count=1 -race ./tests/integration -run TestJobsDispatcherRollbackLeavesOutboxUndispatched
go test -p 1 -count=1 -race ./internal/jobs ./cmd/worker ./tests/integration
go test -p 1 -count=1 -coverpkg=./... -coverprofile=/tmp/qa-coverage.out ./... >/tmp/qa-coverage.log
go tool cover -func=/tmp/qa-coverage.out | tail -1
go vet ./...
test -z "$(gofmt -l cmd internal tests)"
```

Все команды exit0. Targeted SIGTERM0.25s, AT0040.04s; combined race jobs1.019s, worker2.252s, integration16.609s. Full Go coverage **83.9% statements**. Vet/gofmt PASS. Profiles/log находятся только в удалённом одноразовом tool-container и удалены с ним; значения и команды сохранены здесь.

**JQ15 cleanup/runtime PASS:** QA DB count0 после EXIT trap, live migration9, ready200 все checks ok. Метрики: queue_metrics_up1, outbox lag0/backlog0; jobs cancelled0/completed0/dead1/pending0/running0. Existing live dead1 не изменялся и не исследовался как отдельный operational incident.

### Закрытие QA-J17-FAIL и acceptance verdict

76dced6 меняет только test fixture: marker handler-started теперь записывается только для переданного WORKER_SIGTERM_EVENT_ID; посторонние retained test jobs не могут выдать false readiness для нового события. На той же последовательности targeted→AT004→combined suite ранее наблюдавшееся падение не воспроизвелось. **QA-J17-FAIL закрыт ретестом JQ14.** AT004 combined rollback→retry PASS; AT008 recovery/claim PASS; AT012 effect/receipt replay PASS; AT017 SIGTERM→bounded runtime return→hard-stop→expiry recovery PASS в обозначенной модели supervisor escalation.

Предел AT017 остаётся явным: это реальный OS SIGTERM subprocess через production signal/runtime path с test handler, а не самостоятельный normal exit без supervisor SIGKILL. Lease expiry ускоряется SQL; второй pending job/no-new-claims не измеряется отдельным assertion. In-flight API drain исправлен и одобрен source review ec774a9, но отдельный execution сценарий drain не добавлялся.

**Runtime QA verdict: PASS. Overall status: in_qa**, пока оркестратор не зафиксирует оставшиеся gates: короткий независимый test-only review76dced6 и оценку coverage nondecrease. 83.9% превышает абсолютный80%, но ранее сообщённые84.0% относятся к9394f65; сопоставимой baseline/diff-line проверки здесь нет. Разницу нельзя скрывать или называть выполненным nondecrease требованием без проверки. Предыдущие строки rework относятся к историческим попыткам и заменяются JQ14–JQ15 в пределах фактически проверенных сценариев.

Передача: artifacts QA.md; evidence JQ14–JQ15, exact hostedCI+remote; risks — coverage comparison и описанные process/API limits; recommended_next_role — Ревьювер/Оркестратор для закрытия remaining review/coverage gates, затем фиксация завершения.

## Финальное QA-решение — de9f654

**status: completed; verdict: PASS в проверенном SPEC-030/dev scope.** Дата2026-09-12. Exact SHA `de9f65421699c2cddbda3da94db2b1b1e6c73be2`.

**JQ16:** независимый `gh run view 34681058254 --repo NET-BEAR/ohelpdesck --json headSha,conclusion,jobs` — exit0, exact SHA/success; verify и deploy-dev success, включая full backend/database/frontend/OpenAPI verification, build/start, runtime smoke и pinned SSH deployment. Run: https://github.com/NET-BEAR/ohelpdesck/actions/runs/34681058254 . Verify completed07:38:03Z, deploy completed07:39:52Z.

**JQ17:** `gh run view 34681058254 --repo NET-BEAR/ohelpdesck --log` с фильтром coverage — exit0; фактический CI gate сообщает **83.77% statements**, **80.20% executable block lines (2212/2758)**. Оба абсолютных порога80% пройдены. Ранее84.0% относится к другому source snapshot9394f65 и другой записи измерения, а83.9% — к remote Go cover JQ14. Они не являются сопоставимым baseline для вывода о регрессии в de9f654. Одновременно эти числа не доказывают nondecrease за весь исторический SPEC-030 диапазон: для такой проверки нужен один метод и одинаковый baseline; механический historical comparison остаётся follow-up, а не вымышленный PASS.

`git diff --stat 76dced6..de9f654` — exit0, единственный изменённый файл runtime_test.go,10 добавленных строк. QA прочитал diff: добавлен test nil-registration/fixed error, production-код не меняется. Поэтому прежние независимые remote JQ14–JQ15 (включая исправленную последовательность AT017→AT004→combined race, cleanup/live migration9/readiness) сохраняют применимость к production; финальная полная suite свежего CI дополнительно проверяет точный test-only SHA. Новую disruptive suite QA не запускал по явному поручению оркестратора. Финальный deployed SHA здесь подтверждается успешным exact-SHA deploy job, а не повторным live SSH чтением.

Focused reviewer approve76dced6 прочитан: event-specific marker принят, JOB-R1–R5 закрыты. QA-J17-FAIL закрыт JQ14; новых bugs не обнаружено. Для de9f654 использованы QA diff inspection и exact CI; отдельный reviewer rerun этого10-line test-only addition не заявляется.

Итог: GAP-J04/J08/J12 закрыты; GAP-J17 закрыт в документированной модели SIGTERM→bounded runtime return→supervisor hard-stop→recovery. Ограничения первоначальной AT-матрицы сохраняются как явные пределы измерений: не заявляются естественное истечение lease по часам, отсутствие SIGKILL, отдельный pending-job assertion или специальный execution API-drain scenario. Внешняя provider delivery и чужие Telegram изменения не входят в эту приёмку.

**artifacts:** QA.md — AT-матрица, баг-репорт/ретесты и протокол ПСИ. **evidence:** JQ01–JQ17 с финальными JQ14–JQ17. **risks:** перечисленные process/API limits и future comparable coverage baseline; production credential/данные не читались и не изменялись QA. **recommended_next_role:** Оркестратор / Архивариус — зафиксировать итог этапа и последующие задачи. Предыдущие статусы rework/in_qa исторические и superseded этим решением.
