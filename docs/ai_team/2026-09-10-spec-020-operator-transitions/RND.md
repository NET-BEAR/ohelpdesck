# RND: operator transitions

Статус: `ready_for_review`.

Checkout подтверждает, что таблица `conversations` уже содержит `status`, `priority`, `resolved_at`, `snoozed_until`, `version` и PostgreSQL outbox. `ReceiveInbound` использует одну transaction и поднимает version. Отдельного ConversationService и transition tests пока нет.

SPEC-020 задаёт полную допустимую матрицу переходов и требует domain error для запрещённого перехода. Единственная безопасная concurrency policy, утверждённая пользователем: команда с устаревшей `expected_version` не меняет состояние и возвращает `version_conflict`.

Риск: обновление без predicates по version позволит resolve стереть состояние, уже обновлённое inbound. Минимальная защита: `SELECT ... FOR UPDATE`, затем проверка `version` внутри той же transaction, update с increment и outbox append. Любой отказ должен rollback state и event вместе.
