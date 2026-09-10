# SPEC-020: operator transitions Conversation

Статус задачи: `completed`. Дата: 2026-09-10. Владелец: оркестратор.

## Цель

Реализовать отдельную core vertical: оператор меняет status или priority Conversation с optimistic locking по `expected_version`, а committed mutation создаёт durable outbox event в той же PostgreSQL transaction.

## Границы

В срез входят explicit status transitions из SPEC-020: `open→pending/resolved/snoozed`, `pending→open/resolved/snoozed`, `snoozed→open/resolved`, `resolved→open`; independent priority change; DB/server mutation time; stable domain errors. Вне scope: HTTP/UI, ACL membership, assignment, read state, snooze scheduled wakeup, outbound и provider integration.

## Зависимости и решения

Предыдущая core/outbox vertical завершена (`bbe07b9`). DEC-020-02 утверждена пользователем: команда resolve обязана нести `expected_version`; stale command возвращает `version_conflict`; concurrent/new inbound оставляет Conversation открытой. DEC-020-03: time source — PostgreSQL/server, сохраняются instants `timestamptz`.

## Этапы

1. Подтвердить data/state contract и Given/When/Then до кода.
2. Создать RED integration tests на удалённом dev-контуре.
3. Реализовать минимальный application service и outbox events.
4. Проверить CI и полный remote suite, затем независимые review и QA.

## Результат

Реализация, P1 rework, CI/deploy, remote verification, independent review и QA завершены. HTTP/ACL и WakeSnoozed остаются отдельными срезами.

## Открытые решения

Нет блокирующих решений: предельные сроки snooze и HTTP-contract относятся к последующим task slices. Реализация не выбирает их неявно.
