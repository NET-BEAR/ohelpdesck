# Архитектура: operator transitions

Статус: `ready_for_review`.

`internal/core.ConversationService` получает `ChangeStatus(ctx, command)` и `SetPriority(ctx, command)`. Команды provider-neutral; они не содержат HTTP DTO. Внешняя boundary позже авторизует principal, а этот slice не подменяет отсутствующую channel-membership policy.

В одной `WithinTx`: read Conversation `FOR UPDATE` → compare `expected_version` → validate transition/priority/snooze contract → PostgreSQL `clock_timestamp()` → update with `version=version+1` → append versioned redacted outbox event → commit. Lock order продолжает существующий: Channel/membership (когда добавится boundary) → message idempotency → identity → Conversation row → outbox. Operator transition здесь берёт только Conversation row → outbox.

Events статуса нормативны: `conversation.pending`, `conversation.snoozed`, `conversation.resolved`; выход из `resolved` или `snoozed` в `open` — `conversation.reopened`; `pending → open` — `conversation.opened`. `conversation.priority_changed` содержит old/new priority. Каждый payload содержит conversation/channel IDs, old/new value, version, occurred_at и не содержит message text, identity или credentials.
