# Реализация: SPEC-020 Core Domain, первая vertical

Статус: `ready_for_remote_verification`. Дата: 2026-09-10.

## Граница изменения

Добавлены checksummed migration `000003_core_outbox`, provider-neutral
`NormalizedInboundMessage`, application service `ReceiveInbound` и реальный
PostgreSQL `outbox_events` writer. Внешний webhook, provider SDK, worker,
operator HTTP/API и inbox UI в этот change-set не входят.

`ReceiveInbound` владеет одной PostgreSQL transaction: блокирует активный
Channel и отсутствующие Identity/Conversation через transaction advisory locks,
создаёт Contact/Identity/Conversation/immutable incoming Message, открывает
Conversation и начинает waiting episode по DB `clock_timestamp()`, затем
сохраняет redacted outbox events в той же transaction. Ошибка outbox writer-а
откатывает всю mutation.

## RED evidence

До production-кода в `HEAD` отсутствовал integration-test file этой vertical:

```text
git show HEAD:tests/integration/core_domain_test.go >/dev/null
exit code: 128
```

Это подтверждает RED structural gap: не существовали ни core package, ни
проверяемый ReceiveInbound contract. Добавлены до remote GREEN-проверки tests
для создания facts/outbox, повторной доставки, disabled Channel, concurrent
duplicate delivery и failpoint на `outbox.Append` с проверкой полного rollback.

## Локальная проверка

`git diff --check` завершилась с `exit code 0`.

Локальный Go toolchain отсутствует (`gofmt: command not found`), а пользователь
явно потребовал запускать все тесты на удалённом dev-контуре. Поэтому локальные
`go test`, Compose и integration tests сознательно не выполнялись. Ни один
результат ниже не заявлен как GREEN до исполнения на remote dev.

## Обязательная удалённая проверка

После доставки revision на dev-host, из `/opt/ohelpdesck-dev/current` выполнить
только tools-контейнер проекта; `.env` не выводить и не читать:

```sh
cd /opt/ohelpdesck-dev/current
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml up -d --wait postgres redis minio
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm minio-init
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm migrate up
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go test -count=1 -race ./tests/integration
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go tool cover -func=coverage.out
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go go vet ./...
docker compose --env-file /opt/ohelpdesck-dev/shared/runtime.env -f deploy/compose.yml run --rm go sh -ec 'test -z "$(gofmt -l cmd internal tests)"'
```

После команд нужны их exit codes, проверка coverage gate и отдельные
independent review/QA. Первая migration является expand-only; `migrate down` на
remote с business facts не выполняется.

## Открытые ограничения

Этим срезом не закрываются ACL membership, operator transitions, outgoing
idempotency, full SPEC-030 dispatcher/jobs, HTTP/OpenAPI, metrics и privacy
acceptance. Они остаются downstream задачами из `ACCEPTANCE_TESTS.md`.
