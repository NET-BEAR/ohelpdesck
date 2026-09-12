# Реализация SPEC-030 durable jobs

Статус: `in_implementation`. Дата: 2026-09-11.

## Реализуемый контракт

- `internal/jobs` материализует только явно зарегистрированные event routes. Event без route остаётся с `outbox_events.dispatched_at IS NULL` по DG-030-01.
- Dispatcher блокирует ограниченную выборку events через `FOR UPDATE SKIP LOCKED`, записывает по одной `domain_event` job для пары `(event_id, handler)` и ставит `dispatched_at` в той же transaction.
- Job ownership использует PostgreSQL lease, новый `lease_token` при каждом claim и fenced predicates по `id`, `locked_by`, `lease_token` для complete/reschedule/extend. Просроченные leases восстанавливаются в `pending`.
- DB-only handler сначала создаёт receipt, затем в той же transaction применяет effect. Повторный запуск при уже committed receipt не повторяет effect.
- Worker runtime запускается только в `worker=true`; registry в production пока пустой, поэтому эта vertical не отправляет сообщения во внешний provider и не переводит Message в `sent`/`failed`.

## Схема и совместимость

В рабочем дереве параллельный channel slice занял миграции `000006`–`000008`. Durable jobs поэтому добавляются как additive `000009_durable_jobs`; это требует согласованного обновления реестра миграций, foundation test и task artifacts оркестратором. Таблицы: `jobs`, `event_handler_receipts`, enum `job_status` и индексы claim/recovery/dedup.

## Проверки

Добавлены PostgreSQL integration scenarios для fan-out, retained unknown event, concurrent dispatch, concurrent claim, expired-lease recovery with stale fencing, retry/dead and receipt deduplication. До публикации production SHA они требуют remote RED/GREEN execution на dev. Локально выполнены только `gofmt` и `go build` для `internal/jobs` и `internal/platform/runtime`; integration tests локально не запускались.

## Ограничения

В этой vertical отсутствуют provider client, credentials, network effect, external idempotency/reconciliation, UI dead-letter actions и production durable routes. Delivery имеет семантику at-least-once для future handler; receipt доказывает только at-most-once DB effect внутри одной PostgreSQL transaction.
