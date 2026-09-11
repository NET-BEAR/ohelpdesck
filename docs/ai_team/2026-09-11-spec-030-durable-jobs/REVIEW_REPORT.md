# Независимый review SPEC-030 durable jobs

Дата: 2026-09-11. Verdict: **needs_changes**. Статус: `rework`.

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
