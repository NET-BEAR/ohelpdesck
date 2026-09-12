# Независимый review runtime SPEC-000

Дата: 2026-09-10. Статус: approved. Актуальный verdict после повторного review `0715a6e`: **approve** для runtime/backend/web SPEC-000. Ниже сохранена история исходных findings и промежуточных needs_changes.

## Scope и источники

Проверены backend и web из commits `72d1c5b` / `9e962df` и текущего дерева: SPEC-000, ACCEPTANCE_TESTS.md, ARCHITECTURE.md, IMPLEMENTATION_BACKEND.md, IMPLEMENTATION_WEB.md, platform packages, cmd, migrations, integration tests, frontend client/UI/tests и OpenAPI. CI/CD, deploy, Makefile проверяет другой reviewer. Исправление module path и dependency audit поручены разработчику отдельно.

`docs/agents/roles/reviewer.md` отсутствует в текущем checkout. Memory MCP, `.env`, secrets и внешние системы не использовались. Production-код не изменялся. Findings основаны на исходниках проекта и фактически закреплённых dependency sources из локального Go module cache; воспроизводящий production-сценарий для findings ещё должен быть добавлен разработчиком и проверен QA.

## Findings по приоритету

### R1 — P1: OTLP ошибки обходят redaction и выводят provider response body

Место: `internal/platform/telemetry/telemetry.go:25–35`.

`Init` устанавливает exporter/provider, но не безопасный `otel.ErrorHandler`. В закреплённом `otlptracehttp@v1.34.0/client.go:198–225` ответ collector при ошибке читается целиком и включается в error вместе с endpoint. `sdk@v1.34.0/trace/batch_span_processor.go` передаёт фоновые ошибки в `otel.Handle`; default `otel@v1.34.0/internal/global/handler.go` вызывает `log.Print(err)`. Поэтому collector HTTP 400 с чувствительным body пишет этот body в stderr, минуя `logging.New`. Безопасное сообщение при Shutdown не защищает фоновый batch export.

Нарушение: project rule «не логировать provider responses», ARCHITECTURE trust boundary Runtime → telemetry, F08/F12. Установить безопасный error handler до инициализации exporter и исключить сырые сообщения ошибок. Добавить sentinel-тест с тестовым HTTP collector, ошибочным body и принудительным фоновым export; проверять stdout/stderr, а не только slog capture. Ошибка exporter должна оставаться наблюдаемой и не останавливать процесс.

### R2 — P1: общий outbound client экспортирует query credentials в trace

Место: `internal/platform/telemetry/telemetry.go:62–64`.

`otelhttp.NewTransport` используется без sanitization. Закреплённый `otel@v1.34.0/semconv/internal/v2/http.go:97–105` убирает userinfo, затем сохраняет `req.URL.String()` в `http.url`. Query остаётся полностью. Вызов shared client с `?token=sentinel` или presigned S3 query экспортирует credential. Новый semconv implementation в `otelhttp@v0.59.0/internal/semconv/httpconv.go` также формирует URL из URL.String.

Нарушение: архитектурная граница telemetry без credentials и требование shared safe client. Удалять чувствительные данные из telemetry до export, сохраняя реальный outbound request. Проверить span attributes/status/events in-memory exporter с query token, signature и чувствительным URL path. Текущий default inbound server semconv raw query не записывает; этот finding относится к outbound wrapper. Inbound BasicAuth username попадает в `enduser.id`, поэтому allowlist атрибутов предпочтительнее исправления только одного URL-поля.

### R3 — P1: чувствительный slog Group раскрывает вложенные значения

Место: `internal/platform/logging/logging.go:10–24`.

`ReplaceAttr` игнорирует аргумент `groups`. По реализации/документации Go slog callback вызывается только для non-group attributes. Поэтому `slog.Group("authorization", slog.String("value", "sentinel"))` либо `logger.WithGroup("password").Info("event", "value", "sentinel")` не маскируются: callback видит только ключ `value`. Имеющийся nested test использует secret leaf key и не проверяет чувствительное имя группы.

Нарушение: SEC-FND-003/005, F08 (вложенные поля). Проверять каждый ancestor group и leaf key; добавить оба сценария, включая смешанный регистр и LogValuer, который возвращает группу. Не устранять finding удалением проверки вложенных полей из acceptance.

### R4 — P2: optional Redis может превысить общий deadline readiness

Места: `internal/platform/httpserver/http.go:102–115`; `internal/platform/redis/redis.go:17–21`.

Readiness создаёт 2s context, затем безусловно ждёт каждый результат `<-completed`. Redis client не включает `ContextTimeoutEnabled`; закреплённый `go-redis/v9@v9.7.0/redis.go:633–637` заменяет такой context на Background для socket deadlines. Silent established peer ждёт ReadTimeout=3s, уже превышая budget. Кроме того, `MaxRetries=0` не отключает retries: `options.go:208–211` преобразует 0 в 3. Таким образом необязательный Redis задерживает весь endpoint, несмотря на здоровый PostgreSQL, и может сорвать probe timeout.

Исправить context policy и отключение retries через поддерживаемое значение; collection должен завершаться на deadline с `degraded` для незавершённых optional checks / `unavailable` для PostgreSQL. Добавить тест с TCP peer, который принимает соединение и не отвечает, и проверку времени ответа с разумным допуском. Не ограничиваться immediate connection-refused тестом.

## Дополнительные наблюдения без отдельного blocking finding

- HTTP body cap, request ID sanitization, canonical error и CORS проверены; health не возвращает raw dependency error.
- PG tracer не сохраняет SQL/arguments. Transaction helper использует pgx BeginFunc с rollback/commit; текущая foundation migration создаёт только bookkeeping. Forward-compatible migration runner для следующих specs пока не реализован — расширить на их этапе.
- Frontend отделяет API client, нормализует network/server ошибки и имеет ErrorBoundary. Validator слабее OpenAPI: принимает пустые checks и `error`, хотя контракт требует postgres/redis/object_storage с конкретными enum. При корректном нынешнем backend штатный сценарий работает; рекомендовано добавить contract parity test до расширения API.
- CLI API/worker скрывает подробности любых startup ошибок одной общей фразой. В частности, имя отсутствующего обязательного поля теряется, хотя `config.Load` формирует безопасную диагностическую ошибку. Полезно сохранить безопасный диагностический код/имя поля по FR-FND-003.

## Evidence

Независимая команда:

```sh
docker run --rm --network none \
  -v /Users/krassus/github/ohelpdesck:/src:ro \
  -v ohelpdesck-dev_go-mod:/go/pkg/mod \
  -v ohelpdesck-dev_go-build:/root/.cache/go-build \
  -w /src golang:1.25.7-bookworm go test ./internal/platform/...
```

Exit 0: config, database, httpserver, logging, redis, runtime, storage, telemetry проходят. Production source mount read-only. Это unit evidence; integration, реальный stop/restart PG и process SIGTERM здесь не выполнялись. Отчёт разработчика заявляет full test/vet/race exit 0 и coverage 84.4% statements / 82.85% executable block ranges; эти числа не пересчитаны reviewer. Source audit закреплённых SDK подтверждает механизмы R1–R4, но отдельные regression tests на них пока отсутствуют.

## Передача оркестратору

- status: rework; verdict: needs_changes.
- artifacts: `docs/ai_team/2026-09-10-project-foundation/REVIEW_REPORT.md`.
- evidence: независимые unit tests exit 0; сверка spec/AC/code/tests/Go SDK sources; R1–R4 с конкретными местами и сценариями.
- risks: секреты в логах/trace при описанных входах; превышение readiness budget; отдельные acceptance process/integration gates ещё требуют QA. Этот отчёт не утверждает завершение SPEC-000 или готовность dev deployment.
- recommended_next_role: Разработчик → повторный независимый Ревьювер → QA-инженер.

## Повторный review: 1259562 и 7cff225

Исходные findings выше сохранены как история. На этой промежуточной ревизии verdict остаётся **needs_changes**.

- Web `1259562`: замечание о validator закрыто. Проверяются все три обязательных check, допустимые состояния successful readiness и отсутствие дополнительных checks. Независимая команда `docker run --rm --network none -v /Users/krassus/github/ohelpdesck/web:/src:ro -w /src node:22.22.0-bookworm-slim node node_modules/vitest/vitest.mjs run --coverage.enabled=false --configLoader runner` — **exit 0, 27/27 tests**. Verdict для web: approve.
- Backend `7cff225`: R1 исправлен безопасным OTel ErrorHandler; R3 исправлен ancestor masking; R4 исправлен select по deadline, Redis ContextTimeoutEnabled и MaxRetries=-1. Новые regression tests проверяют группы, зависший Check и silent TCP peer. Typed config.Error сохраняет безопасное имя отсутствующего поля в startup stderr.
- R2 исправлен для query/userinfo/enduser.id/status/exception attributes, но URL path остаётся: `internal/platform/telemetry/redaction.go:56–65` сохраняет Path/RawPath. `/botTOKEN/...` по-прежнему оказывается в span. Нужен дополнительный path sentinel и очистка raw path без изменения реального сетевого запроса. R1 actual collector HTTP error test предпочтительнее только прямого `otel.Handle`.

### R5 — P2: новые subprocess tests зависят от integration environment

Места: `cmd/api/main_test.go:15–19`, `cmd/worker/main_test.go:15–19`.

Оба TestMissingConfig обнуляют DATABASE_URL, но не задают остальные обязательные поля. В чистом environment отсутствуют и REDIS_URL/S3 keys; `config.Load` обходит map с неопределённым порядком. Тест требует только DATABASE_URL и случайно падает на корректном сообщении другого отсутствующего поля. Следует задать безопасные dummy значения остальных обязательных полей в child environment, оставив единственное намеренно отсутствующее поле. Тесты должны работать отдельно от integration suite.

Свежая независимая команда `docker run --rm --network none -v /Users/krassus/github/ohelpdesck:/src:ro -v ohelpdesck-dev_go-mod:/go/pkg/mod -v ohelpdesck-dev_go-build:/root/.cache/go-build -w /src golang:1.25.13-bookworm go test -count=1 ./internal/platform/... ./cmd/api ./cmd/worker` — **exit 1**. Все platform packages прошли; `cmd/api TestMissingConfig` упал: `expected safe missing-field message; got required configuration: REDIS_URL`. Worker на этом запуске прошёл, но имеет тот же дефект теста. Findings переданы оркестратору до final verification.

## Итоговый повторный review: 0715a6e

Актуальный verdict: **approve** для проверенного runtime/backend/web scope. R1–R5 закрыты; новых blocking findings нет.

- R1: добавлен `TestActualCollectorFailureRedaction`, который отправляет настоящий OTLP request в локальный collector, получает HTTP 400 с sentinel body, дожидается export shutdown и проверяет фиксированную диагностическую запись без body/endpoint-path в стандартном log и slog capture.
- R2: `safeAttributes` исключает `url.path`/`http.target`, удаляет Path/RawPath из полного URL вместе с query/userinfo/fragment. В экспортируемом URL остаётся origin. Реальный inbound/outbound regression проверяет отсутствие path/query/BasicAuth sentinels в spans и их сохранение в запросе, полученном HTTP server; tracing остаётся включённым.
- R3: ancestor masking сохранён; callback больше не модифицирует backing array `groups` через append.
- R4: deadline collection и Redis context policy сохранены; целевые race tests проходят.
- R5: child process tests задают безопасные тестовые значения всех остальных обязательных полей. Единственное отсутствующее поле — DATABASE_URL; тесты проходят независимо от integration environment.

Независимая итоговая команда на source tree после `0715a6e`:

```sh
docker run --rm --network none \
  -v /Users/krassus/github/ohelpdesck:/src:ro \
  -v ohelpdesck-dev_go-mod:/go/pkg/mod \
  -v ohelpdesck-dev_go-build:/root/.cache/go-build \
  -w /src golang:1.25.13-bookworm \
  go test -race -count=3 \
  ./internal/platform/telemetry ./internal/platform/logging \
  ./internal/platform/httpserver ./internal/platform/redis \
  ./cmd/api ./cmd/worker
```

**Exit 0**, все шесть packages прошли три запуска с race detector; чистый container environment без deployment credentials, production sources read-only. Web evidence остаётся 27/27 exit 0 на `1259562`; последующие backend commits frontend не меняли. Полный final make verify выполняет оркестратор отдельно.

Итоговая передача:

- status: approved; verdict: approve.
- artifacts: `docs/ai_team/2026-09-10-project-foundation/REVIEW_REPORT.md`.
- evidence: source review `1259562`, `7cff225`, `0715a6e`; независимые web 27/27 и backend targeted `-race -count=3`, exit 0; исходный R5 был воспроизведён до исправления.
- risks: approval относится к коду foundation и проверенным regression scenarios. Реальные process SIGTERM, PostgreSQL stop/recovery, integration/end-to-end dev smoke и финальное общее покрытие подтверждаются отдельным QA/verification; review не заменяет эти gates. Infrastructure/deploy остаются scope другого reviewer. Будущие telemetry attributes/domain spans требуют соблюдения действующей privacy boundary.
- recommended_next_role: QA-инженер.
