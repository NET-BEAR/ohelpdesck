# QA-отчёт: SPEC-020 Core Domain и transactional outbox

Статус: `passed`. Дата проверки: 2026-09-10. Проверяющий: QA-инженер.

## Предмет и граница проверки

Проверена первая поставленная core/outbox vertical из `IMPLEMENTATION.md` на
удалённом dev-контуре. Базовая ревизия production-кода — `6e7b593`; task
review зафиксирован в `2060913`. CI push/deploy run `34503311084` завершился
успешно. Фактический deploy-каталог не содержит `.git`, поэтому соответствие
SHA на host подтверждается указанным CI deployment evidence и образом API
`ohelpdesck-api:6e7b593b6708a2a8e65af2b6216cefa43caa48c7`, показанным
`docker compose ps`.

В область этого среза входят AC `AT-020-020`, `AT-020-024`, `AT-020-025`,
`AT-020-061` (ветка ошибки during outbox append) и durable-outbox часть
`AT-020-062`, а также migration 4, закрывающая замечание review о скрытых
triggers. Проверка не расширяет scope до ACL membership, operator transitions,
outgoing, worker/jobs, HTTP ingress, metrics и privacy-сценариев из полного
`ACCEPTANCE_TESTS.md` — они явно отложены `IMPLEMENTATION.md`.

## Environment

- Remote dev: `root@vm742476.vps.masterhost.tech` по SSH ключу, предоставленному
  для проекта.
- Рабочий каталог: `/opt/ohelpdesck-dev/current`.
- Команды исполнялись только в project Compose tools-container c env-file
  `/opt/ohelpdesck-dev/shared/runtime.env`; его содержимое не читалось и не
  выводилось.
- PostgreSQL, Redis, MinIO, API и Web были запущены. API/Web Docker healthcheck
  имели статус `healthy`.

## Acceptance evidence

| Проверка | Evidence | Результат |
|---|---|---|
| Migration 4 | `migrate up` сообщил `migration version: 4`; повторное применение завершилось без ошибки. | PASS |
| Последовательный duplicate с изменёнными sender/thread | Remote `TestReceiveInboundDuplicateDoesNotCreateAdditionalFacts`: возвращается canonical result, нет лишних Contact/Identity/Conversation/Message/events и version increment. | PASS |
| Конкурентный duplicate с изменёнными sender/thread | Remote `TestReceiveInboundDuplicateRaceReturnsCanonicalMessage` под `-race`: один canonical result, без unique error и лишних facts/events/version. | PASS |
| Неактивный Channel | Remote `TestReceiveInboundRejectsInactiveChannelWithoutFacts`: вход отклоняется, факты и outbox не создаются. | PASS |
| Ошибка при outbox append | Remote `TestReceiveInboundRollsBackWhenOutboxAppendFails`: transaction откатывается без частичных core/outbox rows. | PASS |
| Durable outbox при успешном inbound | В полном integration suite проходят `TestReceiveInboundCreatesCoreFactsAndOutbox` и проверки undispatched event; факты и outbox сохраняются транзакционно. | PASS |
| Runtime readiness | `GET http://127.0.0.1:18081/health/ready` вернул HTTP 200 и `status: ready`, с `postgres`, `redis`, `object_storage` = `ok`. | PASS |

## Команды и результаты

Все команды ниже выполнены на remote dev из `/opt/ohelpdesck-dev/current`;
во все compose-команды передавался только путь к env-file выше.

```text
migrate up
exit code: 0
migration version: 4

 go test -count=1 -race ./tests/integration
exit code: 0
ok github.com/NET-BEAR/ohelpdesck/tests/integration 8.948s

 go test -count=1 -race ./tests/integration -run
 TestReceiveInbound(DuplicateDoesNotCreateAdditionalFacts|DuplicateRaceReturnsCanonicalMessage|RejectsInactiveChannelWithoutFacts|RollsBackWhenOutboxAppendFails)$
exit code: 0
ok github.com/NET-BEAR/ohelpdesck/tests/integration 1.205s

 go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
exit code: 0

 go tool cover -func=coverage.out
exit code: 0
total: 83.6% of statements
internal/core/inbound.go ReceiveInbound: 68.7%
internal/core/inbound.go findCanonicalInbound: 90.9%
internal/outbox/outbox.go Append: 71.4%

 go vet ./...
exit code: 0

 test -z "$(gofmt -l cmd internal tests)"
exit code: 0

 curl --fail --silent --show-error http://127.0.0.1:18081/health/ready
exit code: 0
{"checks":{"object_storage":"ok","postgres":"ok","redis":"ok"},"status":"ready"}
```

## Найденные дефекты и ретест

Новых дефектов не обнаружено. P1 из первоначального review — создание лишних
facts при duplicate inbound с изменёнными sender/thread — ретестирован
последовательным и конкурентным сценариями и закрыт. Migration 4 применена
как отдельная forward migration, поэтому уже checksummed migration 3 не
переписывалась.

## Риски и ограничения

1. Удалённый deploy-каталог не является Git checkout. SHA подтверждён CI
   deploy run и тегом контейнерного образа, но `git rev-parse` на host выполнить
   невозможно.
2. Полный `ACCEPTANCE_TESTS.md` является контрактом дальнейшего SPEC-020:
   не реализованные в этом vertical ACL, operator, outbound, dispatcher/jobs,
   metrics и privacy acceptance cases не следует считать протестированными.
3. Review отметил неблокирующий documentation follow-up: до следующей core
   mutation нужно синхронизировать lock order в `ARCHITECTURE.md` с фактическим
   порядком `Channel → message-idempotency → identity → conversation`.
4. В репозитории не найден файл `docs/agents/roles/qa-engineer.md`, указанный
   в входной инструкции роли; использованы task artifacts, project AGENTS.md,
   acceptance criteria, implementation и review как первичные источники.

## Готовность к ПСИ

Для поставленной inbound/outbox vertical подготовлены remote evidence,
команды, exit codes, health result и coverage. Срез готов к ПСИ в обозначенной
границе после обновления orchestration status. Расширенное ПСИ по полному
SPEC-020 требует реализации отложенных бизнес-функций.
