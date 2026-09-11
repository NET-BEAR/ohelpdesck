# Независимый review: первый vertical slice channel adapters

Статус: `needs_changes`. Дата: 2026-09-11.  
Reviewer ID: `channel_admin_review`. Reviewed commit: `b80e44959b973f260be1f8759ff4487dd392c58c` плюс незакоммиченный diff, относящийся к channel administration.

## Scope и evidence

Проверены `PLAN.md`, `ARCHITECTURE.md`, `ACCEPTANCE_TESTS.md`, `DECISIONS.md`, изменённые runtime/config/auth/OpenAPI/SQL файлы и `internal/channels`. Нерелевантные пользовательские изменения `.gitignore`, `docs/ai_team/PLANS.md`, durable-jobs документации и Chatwoot UI не оценивались.

- `git diff --check` — exit 0.
- `docker run ... golang:1.25.13-bookworm go test ./internal/channels ./internal/platform/config ./internal/auth ./internal/platform/runtime` — exit 0.
- `docker run ... python:3.12.12-slim ... deploy/validate_openapi.py` — exit 0 (`OpenAPI valid; negative validator control rejected`).
- Полный integration/coverage/race suite не запускался: текущая среда не содержит локального `go`, а compose preflight требует отсутствующие deployment secrets. Секреты не читались и не создавались.

## Findings

### P1 — Enable переводит произвольный, непроверенный канал в `active`

**Места:** `internal/channels/service.go:103-123`, `internal/channels/service.go:136-149`, `internal/channels/service.go:220-232`.

Проверка сводится к тому, что `config` и `credentials` являются любыми JSON-объектами (включая `{}`), а тип есть в локальном registry. После этого `Enable` без adapter validation, registration и bounded provider call выставляет `enabled=true,status=active`. Это противоречит AT-CHA-003/004 и архитектурному контракту: active возможен только после provider validation и успешной регистрации webhook/subscription. Например, пустые `{}` для `telegram_bot` создаются, считаются valid и включаются, хотя token отсутствует и adapter вообще не существует.

**Исправление:** до появления конкретного adapter запретить enable для типа без зарегистрированного implementation; добавить type-specific config/credential validation. После появления adapter выполнить validation и registration в одном контролируемом lifecycle, переводя в `active` только после успеха. Нужны RED-тесты на `{}` и provider reject/timeout/registration error.

### P1 — Нет обязательной версии ключа и криптографической привязки credential blob к channel

**Места:** `internal/channels/credentials.go:12-53`, `internal/channels/service.go:68-76`, `internal/channels/service.go:183-187`, `db/migrations/000006_channel_adapters.up.sql:1-4`.

DEC-CHA-02 требует versioned key ID, но schema и ciphertext format хранят только `nonce || ciphertext`; `Config` держит один ключ без key ID. Ротация ключа делает старые credentials нерасшифровываемыми и не позволяет безопасно выполнить постепенную re-encryption. Кроме того AES-GCM вызывается с `nil` additional authenticated data: ciphertext можно переставить между строками/типами channel без ошибки аутентичности, если злоумышленник получил возможность изменять БД.

**Исправление:** добавить durable `credentials_key_id`, keyring/decrypt-by-ID и процедуру rotation; включить stable AAD как минимум из `channel_id`, immutable `channel_type` и формата ciphertext. ID должен создаваться до sealing. Добавить tests: decrypt with old key after rotation, rejection swapped credential blobs и invalid key ID.

### P1 — Миграция объявлена irreversible, но `migrate down` всё равно сообщает успешный rollback

**Места:** `db/migrations/000006_channel_adapters.down.sql:1`, `internal/platform/database/migrations.go:55-83`, `tests/integration/foundation_test.go:40-46`.

`000006.down.sql` является no-op, однако общий runner затем удаляет version 6 из `schema_migrations`. Система рапортует schema version 5, хотя enum `channel_type` по-прежнему содержит значения версии 6. Это ложный успешный rollback и опасная база для rollback/deploy automation; изменённый foundation test закрепляет именно это несоответствие.

**Исправление:** для необратимой migration `down` должен завершаться явной ошибкой до удаления записи migration (или migration должна быть реализована как действительно обратимая с проверенным планом recreate-type/data migration). Добавить integration test, подтверждающий, что version 6 не исчезает после отказанного rollback.

### P1 — Мутации channel не создают требуемого audit evidence

**Места:** `internal/auth/http.go:260-347`.

Архитектура требует actor/correlation audit IDs для create, patch, validate, enable и disable. Обработчики аутентифицируют principal, но не передают actor/correlation в `channels.Service` и не вызывают существующую audit/observability границу. В результате security-sensitive operations с credentials и включением provider нельзя расследовать.

**Исправление:** определить безопасную channel-audit запись (без credential/config payload), сохранять actor ID, action, channel ID, outcome и request/correlation ID только после успешной mutation; покрыть success/rejection tests.

### P2 — Публичный HTTP/OpenAPI контракт расходится с фактическим поведением на malformed channel ID и unavailable service

**Места:** `internal/auth/http.go:290-359`, `internal/channels/service.go:171-180`, `api/openapi.yaml:207-248`.

OpenAPI объявляет UUID path parameter, но handler не выполняет `uuid.Parse`. Значение вроде `/api/v1/channels/not-a-uuid` доходит до PostgreSQL UUID comparison, превращается в generic error и выдаёт 500 вместо documented client validation error. Также handler реально может ответить 503 `channel_administration_unavailable`, а OpenAPI не описывает 503 ни для одного channel endpoint. Actions могут вернуть 400 через `ErrInvalid`, который также отсутствует в contract.

**Исправление:** parse UUID до service call и вернуть 400; дополнить OpenAPI всеми фактическими 400/503 outcomes либо убрать недокументированную unavailable ветвь по согласованному contract. Добавить HTTP negative tests.

## Непроверенные или отсутствующие части acceptance

В diff отсутствуют provider-specific modules, webhook authentication/replay/dedupe, inbound normalization, worker dispatch/reconciliation, status callbacks, UI wizard/accessibility и tests AT-CHA-005--013. Они не могут быть объявлены готовыми в этом vertical slice. До их реализации Channel type не должен имитировать активное внешнее подключение.

## Verdict

`needs_changes`.

Криптографическая база AES-GCM и отсутствие credential в response покрыты локальными тестами, но четыре P1 нарушают утверждённые security/lifecycle/migration guarantees. После исправлений требуется повторный независимый review, затем QA с реальным PostgreSQL и provider fakes.
