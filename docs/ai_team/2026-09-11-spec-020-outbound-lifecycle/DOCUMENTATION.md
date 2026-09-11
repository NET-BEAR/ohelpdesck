# Outbound reply: idempotency и lifecycle

Статус: `draft`; ожидаются удалённые RED/GREEN, CI, независимый review и QA.

## Контракт

`POST /api/v1/conversations/{id}/messages` создаёт provider-neutral намерение
отправить ответ агента. Запрос требует действующую session cookie,
`X-CSRF-Token`, effective permission `conversation.reply`, Channel membership
с `can_reply=true` и непустой header `Idempotency-Key`.

Тело принимает `text`, `html` и опциональный `reply_to_message_id`; хотя бы
одно из text/html должно содержать непустое значение. Успешное первое создание
возвращает `201`, Message в статусе `queued` и `duplicate=false`. Повтор того
же actor, ключа и канонически эквивалентного request возвращает `200` и
канонический Message с `duplicate=true`; второй Message и outbox event не
создаются. Использование ключа с иным conversation, text, html или reply target
возвращает `409 idempotency_conflict`.

Идемпотентность хранится в `message_idempotency_keys` с primary key
`(user_id, idempotency_key)`, SHA-256 canonical request hash и FK на Message.
Транзакция блокирует Conversation и membership, затем advisory lock actor/key,
создаёт immutable outgoing agent Message `queued`, idempotency record и
`message.queued` outbox event.

## Worker-facing lifecycle

`core.OutboundService.MarkSent` допускает только `queued → sent`. Он получает
время из PostgreSQL `clock_timestamp()`, сохраняет `sent_at`, обновляет
activity/outbound timestamps и создаёт `message.sent`. Если Conversation
ожидает клиента, transition закрывает waiting episode, однократно заполняет
`first_response_at` и сохраняет Message как closure.

`MarkFailed` допускает `queued → failed` и definitive `sent → failed`.
Очередной failed Message не очищает waiting. При коррекции sent Message waiting
восстанавливается только когда этот Message всё ещё закрывает текущий episode и
новый episode не начался; событие называется `message.delivery_corrected` и
содержит `waiting_restored` без текста или provider payload. Для точного
восстановления миграция сохраняет `conversations.waiting_closed_since` — начало
эпизода, утраченное после обычного очищения `waiting_since`.

Внешнего provider send, retry policy, delivery/read receipts, jobs и UI в этом
срезе нет.

## Проверки

До production-кода добавлен `tests/integration/outbound_lifecycle_test.go`,
который покрывает user-scoped same/different-key idempotency, concurrent retry,
membership, queued/sent/failed transitions, correction, rollback outbox и HTTP
CSRF contract. Локальные тесты намеренно не запускались по решению пользователя:
RED/GREEN и регрессия должны быть выполнены на удалённом dev-контуре.

Локально выполнены только форматирование Go в `golang:1.25.13-bookworm`,
`git diff --check` и `go build ./...` в том же image. Сборка завершилась с
кодом 0.
