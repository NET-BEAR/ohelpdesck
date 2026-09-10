# Backend SPEC-000: реализация и доказательства

Статус: ready_for_review. Это отчёт разработчика, не независимый review или QA.

## Scope и интерфейсы

Go 1.25.0, воспроизводимый builder `golang:1.25.7-bookworm` (получен digest `sha256:564e366a28ad1d70f460a2b97d1d299a562f08707eb0ecb24b659e5bd6c108e1`). Binaries: `go build ./cmd/api`, `./cmd/worker`, `./cmd/migrate`. CLI миграций принимает `up|down|status`; SQL встроен через embed, filesystem working directory не требуется. Только migration bookkeeping, бизнес-таблицы отсутствуют.

Конфигурация загружается один раз: обязательны `DATABASE_URL`, `REDIS_URL`, `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`. Опции: `ENVIRONMENT` development/test/staging/production; `HTTP_ADDRESS` :8080; `METRICS_ADDRESS` :9090; `S3_USE_SSL` false; `DATABASE_MAX_CONNECTIONS` 10; `HTTP_READ_TIMEOUT` 10s; `HTTP_WRITE_TIMEOUT` 15s; `HTTP_IDLE_TIMEOUT` 60s; `SHUTDOWN_TIMEOUT` 10s; `CORS_ALLOWED_ORIGINS` пустой allowlist; `OTEL_EXPORTER_OTLP_ENDPOINT` необязательный HTTP OTLP endpoint. У production-секретов нет defaults. Из конфигурации не выводятся значения или DSN.

`GET /health/live` не вызывает внешние зависимости. `GET /health/ready` проверяет зависимости параллельно с общим 2s context: PostgreSQL failure → 503/not_ready; Redis/S3 failure → degraded при 200, если PostgreSQL доступен. Request ID валидируется или генерируется, возвращается в заголовке и canonical error. Глобальный body cap 1 MiB, CORS deny-by-default. Metrics только на отдельном listener, не публиковать во внешнюю сеть; сетевую изоляцию обеспечивает deployment.

API и worker проверяют PostgreSQL при старте, не запускают migrations автоматически. Worker пока только lifecycle/metrics, без business jobs. HTTP серверы используют header/read/write/idle timeouts, shutdown закрывает listeners и pool/cache, flush telemetry. PgX transaction helper передаёт владение транзакцией application service. Redis — инфраструктурный клиент, S3 — ObjectStore с Put/Get/Delete/PresignGet/Health.

OTel: HTTP server/outbound wrapper и PostgreSQL query tracer без SQL/arguments в span; optional OTLP HTTP batch exporter, сбой экспортера не останавливает процесс. Shared outbound client имеет deadline, TLS verification, connect/header timeouts, bounded idle pool, User-Agent, trace propagation и запрет redirects.

Логи JSON с service/environment и request/trace context; известные secret-key поля маскируются, произвольные Any объекты не сериализуются. Не передавать секреты в текст сообщения или обычные безопасно названные string-поля.

## RED → GREEN

Перед production-кодом `docker run ... golang:1.25.7-bookworm go test ./...` завершился exit 1: `undefined: Load` в config_test и `undefined: New` в logging_test. После реализации этот набор прошёл. Затем добавлены контрактные HTTP/security, storage/telemetry и реальные integration tests.

Real integration запускается через `docker compose ... run --rm --volume /tmp/ohelpdesck-foundation-runtime:/src go ...` на network `ohelpdesck-dev_default`; секреты передаются Compose, файл `.env` агентом не открывался и значения не выводились.

Команда проверки: `go test -count=1 -coverprofile=coverage.out -coverpkg=./... ./... && go tool cover -func=coverage.out && go vet ./... && go test -race ./...`. Coverage включает cmd binaries, не исключает слабые участки. Integration tests проверяют migrations down/status/up/up/status, rollback/commit, PostgreSQL connectivity, Redis ping, MinIO roundtrip/presign/delete и lifecycle API/worker. `REQUIRE_INTEGRATION=true` запрещает silent skip при отсутствии переменных.

## Зависимости

Владелец infrastructure dependencies: platform. pgx/v5 — PostgreSQL driver/pool/transactions; go-redis/v9 — Redis protocol; minio-go/v7 — S3 signing/streaming; prometheus/client_golang — совместимые metrics; OTel SDK/contrib/OTLP HTTP — tracing/export. Версии закреплены go.mod/go.sum. HTTP router, config, logging и shutdown используют стандартную библиотеку.

## Ограничения и дальнейшие проверки

Нужны независимые Reviewer и QA, включая реальные SIGTERM binaries, отказ/возврат PostgreSQL и полный dev deployment. In-process cancellation tests не подменяют process smoke. Command entrypoints включены в coverage, но не покрыты unit tests. Unit coverage — statement coverage Go; отчёт по исполняемым строкам рассчитывается отдельно из coverprofile. Spec completion определяет оркестратор после frontend/CI/CD/OpenAPI/QA gates.

## Итоговая проверка разработчика

2026-09-10: полный test/coverage/vet/race command завершился exit 0. Общее statement coverage **84.4%**, включая cmd. По объединению line ranges executable blocks coverprofile: **314/379 = 82.85%**. Это вычисление исполняемых block ranges Go, а не внешняя независимая diff-coverage система. Config/logging/Redis/storage/telemetry функции 100%, database Open 92.9%, migrations 84.6%, runtime 81.6%.

## Security rework после независимого review

Module/import paths исправлены на фактический remote `github.com/NET-BEAR/ohelpdesck`. Начальный `govulncheck@v1.1.4` завершился scanner status 3: 27 достижимых advisories в Go 1.25.7 и шести dependency modules. Достижимость символа не доказывает возможность эксплуатации текущими endpoint: например, pgx advisory требует особого simple-protocol SQL с пользовательским параметром внутри dollar-quoted литерала.

Исправленные версии: Go **1.25.13** / `golang:1.25.13-bookworm`, digest `sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48`; pgx **5.9.2**; go-redis **9.7.3**; OTel/SDK/OTLP **1.43.0**; grpc **1.82.1**; x/net **0.55.0**; x/text **0.39.0**, совместимые транзитивные обновления зафиксированы go.sum. Official advisories: [OTLP response allocation](https://pkg.go.dev/vuln/GO-2026-4985), [pgx placeholder sanitization](https://pkg.go.dev/vuln/GO-2026-5004). После dependency updates `go test ./...`, `go vet ./...`, `govulncheck@v1.1.4 ./...` — exit 0, **0 достижимых уязвимостей**; scanner отдельно сообщает 2 advisories в imported packages и 18 в module dependencies без вызываемых уязвимых символов.

Review regressions сначала подтвердили RED: секреты в `slog.Group("authorization",...)` и `WithGroup("password")`; readiness ожидал некорректный Check дольше 2200ms; Redis silent TCP peer игнорировал 50ms context примерно 3s; production API/worker stderr не называл отсутствующий `DATABASE_URL`.

Исправления: redaction учитывает ancestry групп; OTel ErrorHandler пишет фиксированное сообщение без provider response/error; единая граница экспорта очищает URL query/userinfo, enduser.id и exception details/status. Tracing сохраняется; реальный HTTP запрос сохраняет query и Basic Auth, что проверяется inbound/outbound test с in-memory exporter. Redis использует `ContextTimeoutEnabled=true`, `MaxRetries=-1`; readiness выбирает между результатом Check и ctx.Done и по deadline оставляет PostgreSQL unavailable, optional зависимости degraded. API/worker выводят только типизированную безопасную config.Error с именем поля, остальные ошибки остаются обобщёнными. Production missing-config подтверждается subprocess tests.

Итог security rework: test с real integrations, vet, race и govulncheck@v1.1.4 совместно завершились exit 0. Statements 301/340 = 88.5%; executable block line union 385/441 = 87.30%.

## Повторный review: URL path и настоящий OTLP collector

Регрессия с `/botpath-sentinel/` сначала дала RED: `HTTP span leaks path-sentinel`. Export sanitizer теперь сохраняет от URL только origin; удаляет raw/decoded path, query, userinfo, fragment, атрибуты url.path/http.target. Фактический HTTP path/query/Basic Auth сохраняются, spans по-прежнему создаются. Тест actual collector отправляет настоящий OTLP span в HTTP collector с ответом 400 и секретами в response body/endpoint path; захватывает slog и стандартный log и подтверждает только фиксированную ошибку без sentinel.

Redaction логов больше не вызывает append на переданном callback ancestry slice. Production missing-config subprocess tests задают все остальные обязательные значения явно и не зависят от inherited integration configuration. Targeted `go test -race ./cmd/api ./cmd/worker ./internal/platform/telemetry ./internal/platform/logging` — exit 0. Повторное сканирование зависимостей не требуется: go.mod/go.sum не менялись относительно предыдущего security rework.
