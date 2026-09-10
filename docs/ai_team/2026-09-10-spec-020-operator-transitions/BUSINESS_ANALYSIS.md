# Бизнес-анализ: operator transitions

Статус: `ready_for_review`.

Оператору нужен явный lifecycle тикета без скрытого last-write-wins. `pending` означает ожидание клиента/зависимости, `snoozed` требует deadline, `resolved` завершает текущий issue, `open` требует работы. Priority — независимый сигнал срочности и не меняет status.

| Команда | Preconditions | Результат |
|---|---|---|
| ChangeStatus | Conversation exists, `expected_version` актуален, переход есть в matrix | status/markers/version меняются один раз, event durable |
| Resolve | status `open` или `pending`, current version | `resolved`, `resolved_at` от DB clock, version+1 |
| Snooze | allowed source, non-empty future deadline | `snoozed`, `snoozed_until`, version+1 |
| SetPriority | valid enum, current version | priority меняется без status/markers, version+1 |

Запрещённый transition, stale version и невалидный snooze не создают core/outbox rows. Inbound остаётся owner-ом automatic reopen policy и не меняется этим срезом.
