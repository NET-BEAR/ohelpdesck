# QA — Channel ACL и HTTP boundary

- Проверяемая ревизия: `23b50d69c631ec6b4ea61bd53ef211c981a78b22`
- Дата: 2026-09-11
- Вердикт: **PASS для scope Channel ACL и operator HTTP commands**
- Основания: `ACCEPTANCE_TESTS.md`, implementation в `c643a74..23b50d6`, независимый [REVIEW_REPORT.md](REVIEW_REPORT.md) с verdict approve.

## Environment

Проверка выполнена только на согласованном удалённом dev-контуре `root@vm742476.vps.masterhost.tech`. Активный release path подтверждён как `/opt/ohelpdesck-dev/releases/23b50d69c631ec6b4ea61bd53ef211c981a78b22`; migration version — `4`. Секреты, `.env` и значения runtime-переменных не читались и не выводились.

## Матрица acceptance и evidence

| ID | Сценарий | Метод и evidence | Результат |
|---|---|---|---|
| QA-ACL-01 | Agent с `conversation.reply` и `can_reply=true` меняет assigned-to-another-agent Conversation | Remote `go test -count=1 -race ./tests/integration -run 'TestOperatorConversationHTTP'`; сценарий создаёт Conversation, назначает другого агента и получает HTTP 200 | PASS |
| QA-ACL-02 | Нет Channel membership или `can_reply=false` | Тот же remote HTTP integration test: status и priority возвращают 403; snapshot Conversation и число outbox events не меняются до/после | PASS |
| QA-ACL-03 | Отзыв membership перед locked write не оставляет частичную мутацию | Remote HTTP integration test меняет `can_reply=false`, получает 403 и сверяет неизменность aggregate/outbox | PASS |
| QA-ACL-04 | Stale `expected_version` не меняет текущее состояние | Remote HTTP integration test: status request с устаревшей версией возвращает 409 после успешного изменения priority | PASS |
| QA-ACL-05 | Нет session / CSRF | Remote HTTP integration test: без session — 401; с session без CSRF — 403 | PASS |
| QA-ACL-06 | HTTP validation и CORS | Remote HTTP integration test: malformed JSON, UUID, enum, zero version, invalid route, absent Conversation, status conflict; отдельный test подтверждает `PATCH` и `X-CSRF-Token` в preflight | PASS |
| QA-ACL-07 | OpenAPI contract | Инспекция `api/openapi.yaml`: оба PATCH endpoint содержат session cookie, required CSRF, UUID, strict request schema и outcomes 400/401/403/404/409. CI run `34532417247` на `23b50d6` успешно выполнил positive/negative OpenAPI validation | PASS с ограничением remote validator ниже |
| QA-ACL-08 | Регрессия и качество Go | Remote `go test -count=1 -race ./tests/integration` — exit 0; `go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...` — exit 0; `python3 deploy/check_coverage.py coverage.out` — `84.07%` statements, `80.92%` executable block lines; `go vet ./...` — exit 0; `gofmt -l cmd internal tests` — пустой вывод | PASS |
| QA-ACL-09 | Runtime readiness | Remote release SHA assertion, API live, web readiness и web root checks — exit 0; api/web/postgres/redis healthy, worker running | PASS |

## Команды и результаты

Все функциональные команды запускались из текущего release на dev-host через Compose с закрытым runtime env-file, без чтения его содержимого.

- `docker compose … run --rm migrate up` — exit 0, `migration version: 4`.
- `docker compose … run --rm go go test -count=1 -race ./tests/integration -run 'TestOperatorConversationHTTP'` — exit 0, `ok`.
- `docker compose … run --rm go go test -count=1 -race ./tests/integration` — exit 0, `ok`, 12.426 s.
- `docker compose … run --rm go go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...` — exit 0; coverage gate: 84.07% statements / 80.92% executable block lines (1251/1546).
- `docker compose … run --rm go go vet ./...` — exit 0.
- `docker compose … run --rm go sh -ec 'test -z "$(gofmt -l cmd internal tests)"'` — exit 0.
- Runtime smoke against host-loopback published ports: API live, web ready и web root — exit 0.

## Дефекты и ретест

Продуктовых дефектов не выявлено, баг-репорты не создавались.

`python3 deploy/validate_openapi.py` на dev-host завершился с `ModuleNotFoundError: openapi_spec_validator`: это отсутствие CI-only Python dependency в release host, а не дефект API/OpenAPI контракта. Пакет не устанавливался и окружение не менялось. Валидация OpenAPI подтверждена successful CI run `34532417247`; remote дополнительно подтвердил YAML-совместимость и работающий HTTP contract integration suite.

## Материалы ПСИ

Для ПСИ готовы acceptance matrix QA-ACL-01…09, точный delivered SHA, remote command evidence, migration version и runtime health. Операторские endpoints принимаются только как mutation boundary; Conversation list/get/read и отдельная `conversation.read` / `can_read` policy остаются вне данного scope.

status: completed
artifacts: `docs/ai_team/2026-09-10-spec-020-channel-acl-http/QA.md`
evidence: QA-ACL-01…09; remote release `23b50d69c631ec6b4ea61bd53ef211c981a78b22`; CI `34532417247`
risks: OpenAPI schema validator не установлен на dev-host; evidence его выполнения относится к CI. Read/list boundary и can_read не входят в реализованный scope.
recommended_next_role: Оркестратор — зафиксировать завершение vertical slice в планах и выбрать следующий согласованный slice SPEC-020.
