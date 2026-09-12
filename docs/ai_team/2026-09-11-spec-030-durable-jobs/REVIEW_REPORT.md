# Независимый review SPEC-030 durable jobs

Дата первоначального review: 2026-09-11. Актуальный verdict после focused re-review `de9f654` (2026-09-12): **approve**. Статус отчёта: `approved`. JOB-R1..R5 закрыты в проверенном коде; ниже сохранена история проверок и ограничения acceptance evidence.

Проверенная ревизия: `a4fbea218bb1bcfae4857f4dca54d6b9d3e87077`. Диапазон изменений: `ceef027..a4fbea2`. Проверены committed-файлы через `git show`/`git diff`, требования `spec/030-events-jobs-outbox.md`, task artifacts, migration 9, jobs repository/worker, runtime и integration tests. Незакоммиченный Telegram/channel/UI scope не использован для verdict и не изменён. `docs/agents/roles/reviewer.md` отсутствует. Memory MCP, `.env` и секреты не читались.

Важно: базовая jobs implementation и migration 9 уже присутствуют в `ceef027`. Findings ниже указывают пробелы готовности всей SPEC-030, а не утверждают, что все они впервые внесены последним diff. В самом diff исправлены SQL parameter typing, добавлен PostCommitHandler и расширены тесты.

## Findings

### JOB-R1 — P1: recovery последней попытки создаёт запись, блокирующую следующие claims

Места на проверенном SHA: `internal/jobs/jobs.go:202`, `:231`; `db/migrations/000009_durable_jobs.up.sql`, constraint `jobs_attempts_valid`.

`RecoverExpired` безусловно возвращает истёкшую running job в pending. `Claim` выбирает её без проверки бюджета и увеличивает attempts. Migration 9 запрещает attempts > max_attempts. Воспроизводимый сценарий: job с max_attempts=1 → claim (attempts=1) → смерть worker/истечение lease → recovery (pending, attempts=1) → следующий claim пытается записать attempts=2 и нарушает CHECK. Запись остаётся pending; при прежнем priority/run_at она снова попадает первой. `Worker.Run` возвращает ошибку, а runtime завершает процесс. Это нарушает AC-EVT-005/008 и может остановить обработку остальных jobs.

Требуется: exhausted running jobs при recovery переводить в dead с очищенной lease и безопасной причиной; защитить claim от некорректных pending jobs с исчерпанным бюджетом. Remote regression должен проверить max_attempts=1, expiry/recovery, отсутствие SQL error, dead state и успешный claim следующей здоровой job. Имеющиеся tests проверяют exhaustion через Reschedule, но не crash на последней попытке.

### JOB-R2 — P1: typed handler error сохраняет секреты в jobs.last_error_*

Места: `internal/jobs/jobs.go:241–248`, `:267–282`, `:399–404`.

Для обычного error Worker формирует фиксированное сообщение. Для `*JobError` Code/Message передаются в persistence без проверки происхождения; `sanitize` лишь обрезает пробелы и длину. `JobError{Code:"provider_error", Message:"credential=sentinel"}` сохраняет sentinel в last_error_message. Аналогично произвольное значение Code проходит в БД. Это не соответствует AC-EVT-017 и AT-JOB-014, прямо требующим отсутствия configured test secret в persisted errors.

Требуется: persisted code — из явного стабильного набора, message — из безопасного mapping/фиксированного текста; Cause и произвольный Message не сохранять verbatim. Добавить remote sentinel regression для typed и untyped ошибок, включая code/message, и проверить БД/log capture. Тесты текущего SHA проверяют длину и defaults, но не это требование. Пустой production registry снижает текущую достижимость, однако не делает boundary безопасной для первого зарегистрированного handler.

### JOB-R3 — P2: retry policy не реализует обязательный jitter

Место: `internal/jobs/jobs.go:407–422`.

Код прямо выбирает no-jitter policy; задержки равны 1s/2s/4s/.../60s. Одновременно упавшие jobs повторяются синхронно. SPEC-030 AC-EVT-006 и task AT-JOB-009/ARCHITECTURE требуют bounded jitter, а согласованного отказа от него в DECISIONS нет. Имеющиеся тесты закрепляют точные детерминированные значения вместо диапазона jitter.

Требуется внедрить управляемый источник jitter и bounded policy с сохранением RetryAfter/cap semantics; тесты проверяют границы и разнообразие/детерминируемый injected source. Либо отдельное решение пользователя об изменении acceptance, после которого нужен повторный review изменённого scope.

### JOB-R4 — P2: отсутствуют требуемые queue/outbox metrics

Места: `internal/jobs/jobs.go` (Dispatcher/Worker без instrumentation), `internal/platform/runtime/runtime.go:72` (только foundation NewMetrics).

SPEC-030 AC-EVT-018 требует exposed outbox lag и queue depth; ARCHITECTURE дополнительно называет retries/dead/recovery/stale outcomes. В checked SHA jobs не публикует эти метрики, runtime регистрирует только foundation HTTP/process/DB metrics. Поэтому retained unknown events по DG-030-01 остаются данными в PostgreSQL, но обещанная наблюдаемость очереди/лага не реализована.

Требуется минимально expose queue depth и outbox lag через существующий внутренний metrics endpoint с bounded labels; проверить actual metrics output при создании/диспетчеризации job и retained unknown event. Либо явно согласовать перенос acceptance. Это не утверждение об отсутствии самих durable данных.

## Что проверено без отдельного blocking finding

- Dispatcher фильтрует registered event types и не помечает unknown event dispatched; unknown rows не блокируют registered backlog. Outbox locks, fan-out inserts и dispatched_at выполняются в одной transaction; unique partial `(event_id,handler)` защищает duplicate job creation.
- Diff разделяет text placeholder для JSON event_id и UUID-column placeholder: `$3::text` и `$4`. Reschedule использует explicit `$4::job_status`. Переданное remote GREEN подтверждает выполнение этих SQL путей на PostgreSQL.
- Claim использует SKIP LOCKED и свежий token; Complete/Reschedule/Extend проверяют id/status/worker/token. Старый token не меняет новую ownership. Отдельный JOB-R1 относится к exhausted recovery, а не к отсутствию token fencing.
- Receipt вставляется первым через INSERT ON CONFLICT в той же tx, что и DB handler effect. При rollback receipt откатывается; committed receipt предотвращает повторный DB Handle. PostCommitHandler исполняется вне этой tx и может повторяться после receipt — он не даёт exactly-once для сетевых эффектов. Его новый контракт следует документировать явно.
- Migration 9 additive, включает UTC timestamptz, индексы claim/recovery/dedup и обратный порядок down. Down разрушителен для jobs/receipts и не должен автоматически выполняться на populated dev DB.
- Worker создаётся только для worker=true; checked production registry пустой. Runtime startup/smoke не доказывает выполнение настоящего production handler.
- OpenAPI diff исправляет текст description и не вводит новый jobs HTTP endpoint. Незакоммиченный HTTP/channel diff не оценивался.

## Evidence CI и dev delivery

Следующие сведения **предоставлены оркестратором в текущем review**, а не повторно получены reviewer из live-host. Они относятся к `a4fbea2`; секретные значения и raw runtime logs не передавались.

- CI/dev delivery: [GitHub Actions run 34635720836](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34635720836), success, SHA `a4fbea218bb1bcfae4857f4dca54d6b9d3e87077`.
- `/opt/ohelpdesck-dev/current-sha` совпадает с SHA. Remote `docker compose --project-name ohelpdesck-dev --env-file /opt/ohelpdesck-dev/shared/runtime.env -f /opt/ohelpdesck-dev/current/deploy/compose.yml run --rm migrate status` сообщает version 9.
- Remote `go test -count=1 -race ./tests/integration -run TestJobs` и полный `go test -count=1 -race ./tests/integration` — pass.
- Remote `go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...` + `go tool cover -func=coverage.out` — pass; общее statement coverage 83.8%.
- Remote `go vet ./...` и `test -z "$(gofmt -l cmd internal tests)"` — pass.

Reviewer независимо прочитал committed tests и SQL; дополнительные execution tests не запускались, поскольку acceptance разрешает их только на remote dev, а working tree содержит другой незакоммиченный scope. Успешные существующие tests не покрывают JOB-R1/R2. 83.8% общего statement coverage не доказывает changed-line coverage >=80% или покрытие всех acceptance/failure scenarios.

## Конфигурационный инцидент Compose

По evidence оркестратора, старая статическая server compose-конфигурация не содержала required channel key. Candidate завершился fail-closed; оператор обновил configuration manifest из проверенного release с backup, после чего CI dev delivery прошёл. Это подтверждённое восстановление доставки по переданному evidence, но не доказательство runtime correctness очереди.

Проверенный `deploy/compose.yml` требует channel encryption key; patch `deploy/bootstrap.py` генерирует его для нового локального config. Уже существующий config bootstrap не перезаписывает — обновление существующего окружения остаётся явной операцией. Секреты должны сохраняться на host и не включаться в evidence. Для дальнейшего воспроизводимого deploy зафиксировать manifest source/version и безопасный preflight наличия required config до переключения release; не печатать expanded compose environment.

## Документация и оставшиеся acceptance gates

На момент чтения PLAN был `in_design`, DOCUMENTATION — `in_implementation`; PLAN/AC/ARCHITECTURE всё ещё ссылались на migration 6, хотя migration 9 и её регистрация присутствуют в committed code. Оркестратор сообщил, что актуализирует эти ссылки и статусы. Перед completion зафиксировать реальные SHA/run/version9/coverage и новые findings/rework в task artifacts.

Отдельные failure tests требуют подтверждения: rollback после первого fan-out insert; DB business-effect+receipt rollback/replay с crash после commit; завершение blocked handler при shutdown deadline; cancelled/dead eligibility; exhausted expiry; typed-secret sentinel. Тесты receipt с atomic counter подтверждают callback dedup, но не заменяют проверку реального business SQL effect. Полный pass suite не следует выдавать за прохождение всех AT-JOB-001..018.

## Передача оркестратору

- status: `rework`; verdict: `needs_changes`.
- artifacts: `docs/ai_team/2026-09-11-spec-030-durable-jobs/REVIEW_REPORT.md`.
- evidence: независимый committed source/spec/test audit; переданное оркестратором CI/dev GREEN на a4fbea2 и migration9; конкретные uncovered failure paths JOB-R1..R4.
- risks: исчерпанная job блокирует claim/worker; typed error может сохранять credentials; jitter и обязательные queue metrics отсутствуют; coverage/acceptance scope нельзя выводить из общего процента; конфигурационное восстановление требует сохраняемой provenance.
- recommended_next_role: Разработчик → независимый Ревьювер → QA-инженер. До исправления JOB-R1 approve запрещён; остальные gaps также должны быть закрыты либо явно пересогласованы.

## Повторный review 2026-09-12: ceef027..9394f65

Актуальный verdict: **needs_changes** на `9394f6596333b18c7171262c6a0fb1c6470b9752`. История первоначального review выше сохранена. JOB-R1, JOB-R3 и JOB-R4 закрыты в проверенном исходном коде; **JOB-R2 остаётся открытым P1**.

### Закрытые замечания

- **JOB-R1:** Claim теперь выбирает `attempts < max_attempts`; RecoverExpired переводит исчерпавшую бюджет running job в dead и очищает lease. Сценарий poison-row/CHECK violation устранён. Новый `TestJobsRecoveryExhaustedLeaseMarksDeadAndNeverReclaims` проверяет max_attempts=1, expiry, dead и отсутствие повторного claim. Уже существующая некорректная pending job с исчерпанным бюджетом не блокирует очередь благодаря фильтру, но её отдельное исправление/перевод в dead остаётся эксплуатационной задачей, если такая запись успела возникнуть на старой версии.
- **JOB-R3:** retryDelay использует bounded random jitter с injectable source. Предел добавки — min(delay/4, 60s-delay), отрицательные/слишком большие injected значения ограничиваются, RetryAfter и максимальная минута сохраняются. Тесты проверяют deterministic base и bounded injection. На достигнутом cap jitter равен нулю — это не нарушение установленного верхнего ограничения.
- **JOB-R4:** PostgreSQL MetricsReader подключён к существующему NewMetrics в runtime API/worker. Collector имеет общий 1s context, fixed status labels и failure signal `queue_metrics_up=0`, не выводит raw DB error. SQL считает все undispatched events, включая unknown routes, и jobs по status; raw event/handler/ID/payload в labels отсутствуют. Экспортируются `outbox_dispatch_lag_seconds`, `outbox_undispatched_events`, `job_queue_depth`. Изолированный commit `08ac0f5` ранее прошёл отдельный source review; интеграция присутствует в final committed runtime.

### JOB-R2 — P1 остаётся: blacklist маркеров не обеспечивает redaction значения credential

Место: `internal/jobs/jobs.go`, функции `sanitize` / `containsSensitiveValue` и вызов `Reschedule`.

Rework добавляет поиск слов `authorization`, `bearer`, `token`, `secret`, `password`, `credential`, `api_key`, `sentinel`. Произвольное секретное значение не обязано содержать ни одно из них. Например, полностью синтетический `JobError{Code:"provider_error", Message:"h8Qm2v9Lx7"}` проходит проверку и сохраняет message без изменений; такой же ввод в Code тоже сохраняется. Это следует прямо из ветвей sanitize и сохраняет первоначальный дефект AC-EVT-017/AT-JOB-014.

Новые tests используют строки, содержащие blacklist-маркеры, в том числе буквальное `sentinel`; они доказывают лишь обнаружение этих слов, а не отсутствие configured secret в persistence. Требуется исходно рекомендованный явный набор стабильных codes и fixed message mapping, неизвестные codes → `internal_error`/`job failed`; произвольный typed Message и Cause не сохранять verbatim. Проверить как минимум синтетическое непрозрачное значение без ключевых слов в Message при разрешённом Code, а также в неизвестном Code. Не добавлять это конкретное значение в blacklist.

### Quay MinIO и delivery evidence

В `deploy/compose.yml` server и minio-init теперь используют `quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z`. Для init сохранены `/bin/sh -ec` и вызовы `mc`; следовательно нужна фактическая доступность `mc` в выбранном server image. По переданному оркестратором CI/dev success полный runtime успешно стартовал, поэтому этот путь проверен для указанного образа/ревизии. Persistent volumes, credentials expressions, bucket-init retry policy и ports patch не меняет. Новое содержимое ключей reviewer не получал.

Предоставленное оркестратором evidence на `9394f65`:

- [CI/dev run 34647922441](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34647922441): success; SHA `9394f6596333b18c7171262c6a0fb1c6470b9752`.
- Remote migration version 9; targeted recovery/redaction/metrics `-race` — pass; full integration `-race` — pass; full coverage — **84.0% statements**; vet/gofmt — pass.
- Actual `/metrics` содержит обязательные series и `queue_metrics_up=1`.

Дополнительно reviewer независимо выполнил `gh run view 34647922441 --repo NET-BEAR/ohelpdesck --json headSha,conclusion,jobs`, exit 0: headSha совпадает, verify и deploy-dev оба success, включая Build and start complete runtime / Runtime smoke. Remote migration/coverage/metrics результаты имеют provenance сообщения оркестратора. Reviewer сверил final committed implementation и tests, не запускал локальные execution tests и не использовал uncommitted Telegram runtime. GREEN не закрывает JOB-R2, поскольку его новая проверка ограничена marker-bearing inputs. Source-level redaction defect остаётся достаточным основанием needs_changes.

Актуальная передача: status `rework`; artifacts — этот REVIEW_REPORT.md; evidence — source review final range и указанное remote/CI evidence; risks — сохранение непрозрачных credentials через typed errors, необходимость отдельного dev/QA после исправления; recommended_next_role — Разработчик → повторный Ревьювер → QA-инженер.

## Финальный review JOB-R2: 239057b, 2026-09-12

Проверенный SHA: `239057bc0f06297a876aa043eb88171b897e2ad1`. Актуальный verdict: **approve** для проверенного SPEC-030 scope. **JOB-R1–JOB-R4 закрыты**, новых blocking findings нет.

JOB-R2 исправлен по принципу fail-closed. `persistedFailures` задаёт конечный набор стабильных codes и фиксированных messages. `sanitize(code, _ string)` полностью игнорирует raw Message; разрешённый code преобразуется в константную пару, неизвестный — в `internal_error` / `job failed`. `Cause` не участвует в SQL persistence. Слова-маркеры и поиск конкретного sentinel удалены; любой непрозрачный ввод проходит ту же безопасную политику.

Unit regression проверяет разрешённый code с произвольным raw message и неизвестные непрозрачные code/message. PostgreSQL integration regression передаёт непрозрачные Code/Message/Cause без blacklist-маркеров и проверяет фиксированную сохранённую пару. Ожидания ранее существовавших tests обновлены на безопасный mapping, включая exhausted state. JOB-R1 recovery/claim guard, JOB-R3 bounded jitter и JOB-R4 metrics/runtime wiring из предыдущего review этим patch не меняются; их закрытие сохраняется.

Независимое CI evidence: `gh run view 34648800796 --repo NET-BEAR/ohelpdesck --json headSha,conclusion,jobs` — **exit 0**. [Run 34648800796](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34648800796) относится к точному SHA `239057b`; verify и deploy-dev — **success**, включая backend/database/frontend/OpenAPI verification, Build and start complete runtime, Runtime smoke и Deploy tested commit over pinned SSH.

Оркестратор отдельно сообщил remote targeted race GREEN для `TestJobErrorAndSanitize|TestJobsSanitizesBlankFailureAndLeaseExtensionFencing` в unit/integration packages. Reviewer проверил source/tests и CI metadata, не запускал локальные execution tests и не выдаёт переданный remote результат за собственный повторный прогон. Последнее число 84.0% statements относится к предыдущему полному remote прогону на `9394f65`; новый точный процент здесь не заявляется.

Оставшиеся ограничения не отменяют code-review approve: отдельный QA должен зафиксировать итоговое acceptance на текущем dev SHA; метрики и targeted regressions не доказывают external provider delivery; task documentation и rollback/recovery runbook должны отражать финальный код. Если старый runtime успел создать pending job с исчерпанным бюджетом, claim guard предотвращает блокировку, но её эксплуатационный перевод в dead проверяется отдельно. Будущие новые persisted failure codes добавляются только явным безопасным mapping.

Финальная передача:

- status: `approved`; verdict: `approve`.
- artifacts: `docs/ai_team/2026-09-11-spec-030-durable-jobs/REVIEW_REPORT.md`.
- evidence: независимый source review `239057b`, CI metadata exact SHA/verify+deploy success, remote targeted race GREEN с provenance оркестратора; закрытие всех JOB-R1..R4.
- risks: approval ограничен reviewed SPEC-030 scope; общий статус completed определяется после QA, не этим отчётом. Production и чужие uncommitted Telegram/channel изменения не менялись.
- recommended_next_role: QA-инженер.

## Review acceptance-only reroute: d35941a

Дата: 2026-09-12. Диапазон `239057b..d35941a`: только 194 добавленных строки в `tests/integration/jobs_test.go`, production-код не менялся. Verdict для test patch: **approve**. Предыдущий code-review approve и закрытие JOB-R1..R4 сохраняются; это не означает автоматического полного прохождения всех acceptance criteria.

| Критерий | Что подтверждает новый сценарий | Предел доказательства |
|---|---|---|
| AT-JOB-004 | `TestJobsDispatcherRollbackLeavesOutboxUndispatched`: PostgreSQL trigger отклоняет второй handler после первого insert; проверяются отсутствие jobs и NULL dispatched_at. Порядок handlers задаётся Registry сортировкой. | Retry после снятия injected failure и полный canonical fan-out здесь не выполняются. Их нужно связать с отдельным доказательством либо добавить этот шаг; текущий тест закрывает rollback-часть AT004. |
| AT-JOB-008 | `TestJobsConcurrentRecoveryAndClaimRespectsLeaseBudget`: recovery и claim стартуют конкурентно, допускается корректный исход claim до recovery либо после него; затем получена свежая работа с attempts=2 и выполнен Complete. | Это race одной recovery с одним claim, не двух recovery одновременно; фактическое пересечение SQL транзакций зависит от scheduler. Для заданного сценария тест полезен и не требует единственного недетерминированного порядка. |
| AT-JOB-012 | `TestJobsReceiptEffectStaysAtomicAcrossFailedCompleteAndRecovery`: handler делает настоящий SQL insert business-effect table в receipt transaction; Complete отвергается по неправильному token; expiry/recovery/reclaim/replay не повторяет callback/effect; итог — одна effect и одна receipt. | Отказ Complete смоделирован stale token, не смертью процесса. Это достоверное доказательство durable DB replay boundary; rollback после business effect до commit отдельно не проверяется данным сценарием. |
| AT-JOB-017 | `TestJobsWorkerShutdownLeavesClaimForExpiryRecovery`: in-process Worker.Run, дождались начала handler, отменили context; cooperative handler сразу возвращает ctx.Err; worker выходит за 1s, job остаётся running и восстанавливается после искусственного истечения lease. | **Частичное покрытие.** Нет запуска cmd/worker subprocess, отправки SIGTERM, прохождения signal.NotifyContext/runtime shutdown, configured grace timeout или handler, работающего дольше grace. Не проверяется отсутствие новых claims через вторую ожидающую job. Называть это actual SIGTERM или полным AT017 нельзя. |

Независимо выполнено `gh run view 34649755385 --repo NET-BEAR/ohelpdesck --json headSha,conclusion`: **exit 0**, exact SHA `d35941aad2264e123ce18054fdeaa2cebbeac87d`, conclusion **success**. [CI run](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34649755385). Reviewer не запускал локальные execution tests; новых remote измерений shutdown не получал.

Тесты создают общие PostgreSQL trigger/function/table имена и полагаются на cleanup; запускать в выделенной acceptance БД без конкурентного запуска того же набора. Это test fixture, не migration production schema. Ошибки cleanup сейчас игнорируются; после прерванного запуска необходимо проверить отсутствие тестовых объектов перед повторением. Эти ограничения не требуют изменения production-кода.

Передача QA: status `approved` для reroute test patch; artifacts — этот REVIEW_REPORT.md; evidence — независимый source review и CI exact-SHA success; risks — AT004 retry и AT017 process/grace остаются неподтверждёнными указанными тестами. Следующий этап: QA-инженер фиксирует **partial/unverified** для этих частей и получает process-level evidence перед заявлением полного acceptance, либо возвращает недостающее покрытие Разработчику. Общий completion SPEC-030 на основании только этого patch не подтверждается.

## Cumulative SIGTERM/runtime review: 239057b..f3729a5

Дата: 2026-09-12. SHA: `f3729a5d053cb06a1f0562598d4beb0c7d0eeee0`. Verdict: **needs_changes**. JOB-R1..R4 не затронуты и остаются закрыты; новый JOB-R5 требует исправления.

### JOB-R5 — P1: общая shutdown-ветка закрывает API dependencies до HTTP drain

Место: `internal/platform/runtime/runtime.go:163–181` на f3729a5.

`run(ctx, worker, ...)` используется и API, и worker. После сигнала сразу запускаются `db.Close`, `cache.Close` и остановка telemetry; только затем вызывается `s.Shutdown`. PostgreSQL pool закрыт для новых acquisition, Redis client тоже закрывается, пока in-flight API requests ещё выполняются. Запрос, который после чтения тела/проверок должен взять connection или выполнить следующий запрос после предыдущего, получает closed pool/client вместо штатного завершения в grace period. Остановка exporter до окончания HTTP spans также теряет завершающую telemetry.

Это новая регрессия SPEC-000 §17: сначала прекратить приём, дать in-flight requests завершиться, затем закрыть dependencies. Worker fix для некооперативного handler не должен менять порядок API graceful shutdown.

Требуется: остановить listeners и дождаться HTTP drain в рамках общего deadline, затем запускать bounded cleanup ресурсов на оставшемся deadline; сохранить защиту от бесконечного Pool.Close и отдельную worker cancellation policy. Добавить regression с in-flight API handler, который ещё не приобрёл DB connection на момент сигнала и выполняет SQL после начала shutdown, но до конца grace: request должен успешно завершиться. Проверить, что общий elapsed shutdown остаётся bounded при зависшем handler/closer. Reviewer выявил сценарий по source ordering; CI smoke не проверяет in-flight DB request во время SIGTERM.

### Что новый acceptance patch действительно подтверждает

- AT004 дополнен: после rollback снимается trigger, retry создаёт две jobs и dispatched_at, следующий dispatch не создаёт дубликатов. Ранее отмеченный пробел retry закрыт.
- `runWorker` выделяет общий production/test signal boundary с `signal.NotifyContext` для SIGTERM. Helper запускает фактический runtime с test registry; registration hook не включается через внешние пользовательские данные.
- Process-тест дожидается marker начала handler, посылает настоящий `syscall.SIGTERM`, затем ждёт `runtime-returned`. Handler намеренно игнорирует context и спит 5s; runtime настроен на 100ms shutdown и должен вернуть управление за максимум 400ms. Это сильнее прежнего in-process cancellation test и доказывает bounded runtime return для данного noncooperative сценария.
- `signal-observed` marker пишется после ctx.Done и используется в диагностике timeout. На success branch его наличие отдельно не asserted; общий signal path и ожидание запущенного handler обеспечивают полезное доказательство, но документировать отдельную проверку marker на success нельзя.
- После marker runtime-returned тест сам вызывает Process.Kill и ждёт завершения. Значит он проверяет **SIGTERM → bounded runtime return → supervisor-like hard stop → lease recovery**, а не самостоятельный graceful process exit/exit code. Нельзя подменять этой проверкой отсутствие необходимости SIGKILL в штатном cooperative shutdown.
- PostgreSQL проверяется: job после остановки не completed, остаётся running; затем срок lease искусственно сдвигается в прошлое и RecoverExpired возвращает 1. Реальное ожидание lease expiry, второй pending job/no-new-claims и повторное выполнение восстановленной job этим тестом не проверяются. Они остаются scope отдельных сценариев QA.
- Bounded cleanup helper и общая shutdown deadline предотвращают бесконечное ожидание checkouts в проверенном worker path. Но отмеченный порядок cleanup ломает API drain и должен быть исправлен до approve.

### Evidence и передача

Независимая команда `gh run view 34673895183 --repo NET-BEAR/ohelpdesck --json headSha,conclusion,jobs` — **exit 0**. [CI/dev run 34673895183](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34673895183): exact SHA f3729a5; verify и deploy-dev success, включая Build and start complete runtime / Runtime smoke / Deploy tested commit over pinned SSH. Success существующей suite не покрывает JOB-R5. Reviewer не запускал локальные execution tests, не менял production и не использовал чужой uncommitted Telegram diff.

Актуальная передача: status `rework`; verdict `needs_changes`; artifacts — REVIEW_REPORT.md; evidence — source review cumulative range и independently verified CI metadata; risks — in-flight API errors при shutdown, отдельные ограничения process acceptance перечислены выше; recommended_next_role — Разработчик → повторный независимый Ревьювер → QA-инженер.

## Re-review JOB-R5: ec774a9

Дата: 2026-09-12. SHA: `ec774a9b47c98bd0bc1dae2975b5d43b5b525da3`. Verdict: **approve** для reviewed code; JOB-R1..R5 закрыты, новых blocking findings нет.

JOB-R5 исправлен: `run` создаёт единственный shutdown context, сначала вызывает HTTP Shutdown с этим context и принудительно закрывает HTTP server только при ошибке/истечении срока; PostgreSQL, Redis и telemetry cleanup запускаются после завершения drain. Их ожидание использует тот же исходный deadline, новый полный grace не добавляется. In-flight request в пределах drain снова имеет доступ к dependencies; noncooperative worker checkout не блокирует возврат runtime бесконечно. Source ordering соответствует исправлению первоначальной причины.

В SIGTERM test добавлена проверка running state после handler-ready и временное продление lease до 24h, чтобы recovery другого runtime не вмешивался в сценарий. После остановки lease по-прежнему принудительно истекает и recovery проверяется отдельно. Это честная изоляция проверки SIGTERM от часов lease, но не доказательство автоматического истечения lease в реальном времени. Ограничение предыдущего review сохраняется: тест доказывает SIGTERM → bounded runtime return → test-controlled hard-stop → recovery, не самостоятельный normal process exit без SIGKILL.

Независимая команда `gh run view 34676157333 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` — **exit 0**, exact SHA ec774a9, conclusion **success**. [CI run 34676157333](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34676157333). Оркестратор сообщил CI/dev GREEN. Reviewer проверил committed diff f3729a5..ec774a9, не запускал локальные execution tests и не изменял production/чужой Telegram scope.

В rework отсутствует отдельный новый regression с API request, приобретающим DB connection после начала shutdown. Поэтому source-level закрытие JOB-R5 не следует описывать как прохождение именно такого сценария. QA должен отдельно зафиксировать in-flight API drain, cooperative process exit и точный предел process shutdown; имеющееся GREEN относится к текущей suite. Это ограничение доказательств, а не новый обнаруженный дефект исправленного порядка.

Актуальная передача: status `approved`; verdict `approve`; artifacts — REVIEW_REPORT.md; evidence — source ordering review и independent exact-SHA CI success; risks — перечисленные process/API acceptance limits, отдельное QA остаётся обязательным; recommended_next_role — QA-инженер.

## Focused re-review readiness event isolation: 76dced6

Дата: 2026-09-12. SHA: `76dced61cd091284bcc92d6635257608b949dca4`. Verdict: **approve**. Диапазон ec774a9..76dced6 меняет только `cmd/worker/main_test.go`; production shutdown ordering и закрытие JOB-R1..R5 сохраняются.

Родитель передаёт UUID созданного test event через `WORKER_SIGTERM_EVENT_ID`; helper проверяет синтаксис UUID и передаёт его handler. Readiness marker и намеренная 5s блокировка возникают только при совпадении DomainEvent.ID. Старое либо другое событие того же event type больше не может ложно подтвердить запуск нужного handler. Это усиливает привязку последующих проверок running state/SIGTERM/recovery к конкретной test fixture, не отключая фактическое получение работы из PostgreSQL и signal boundary.

Ограничение: несовпавший event handler возвращает nil, поэтому данный helper не является полностью изолированным consumer и может завершать другие jobs с тем же test handler. Как и ранее, запускать его следует в выделенной acceptance БД без конкурентной копии теста; marker isolation не следует выдавать за полную queue isolation.

Независимая команда `gh run view 34678885611 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` — **exit 0**, exact SHA 76dced6, conclusion **success**. [CI run 34678885611](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34678885611). CI/dev GREEN также передан оркестратором. Reviewer проверил focused committed diff, не запускал локальные execution tests и не менял production/чужой uncommitted scope.

Финальная передача: status `approved`; verdict `approve`; artifacts — REVIEW_REPORT.md; evidence — independent focused source review и exact-SHA CI success; risks — прежние пределы SIGTERM/hard-stop/API-drain acceptance и isolated-test-DB requirement сохраняются; recommended_next_role — QA-инженер.

## Финальный test-only review: de9f654

Дата: 2026-09-12. SHA: `de9f65421699c2cddbda3da94db2b1b1e6c73be2`. Verdict: **approve**. Проверен единственный новый `TestRunWorkerWithRegistryRequiresRegistration`: вызов с nil registration должен возвращать ошибку и фиксированное безопасное сообщение `worker registration is required`. Тест соответствует существующей проверке аргумента до конфигурации/подключений. Production не меняется, зависимости для этого сценария не требуются. Новых findings нет; закрытие JOB-R1..R5 сохраняется.

Независимая команда `gh run view 34681058254 --repo NET-BEAR/ohelpdesck --json headSha,conclusion` — **exit 0**, exact SHA de9f654, conclusion **success**. [CI run 34681058254](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34681058254). По переданному оркестратором CI evidence: **83.77% statements / 80.20% executable lines**. Reviewer эти проценты отдельно не пересчитывал. Сопоставимого baseline для вывода об отсутствии снижения не предоставлено; **nondecrease не подтверждён**. Числа не заменяют QA отдельных acceptance scenarios.

Финальная передача на de9f654: status `approved`; verdict `approve`; artifacts — REVIEW_REPORT.md; evidence — focused source review и independent exact-SHA CI success, coverage с указанным provenance; risks — ранее зафиксированные acceptance limits и неподтверждённое сравнение покрытия сохраняются; recommended_next_role — QA-инженер. Production, PLANS.md и чужой uncommitted scope не менялись.
