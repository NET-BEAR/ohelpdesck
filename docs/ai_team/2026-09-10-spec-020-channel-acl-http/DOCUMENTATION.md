# Operator Conversation HTTP API

Статус: `draft`, ожидает удалённую проверку и независимые review/QA.

Операторские mutation endpoints работают только с server-side session cookie
`ohelpdesck_session` и header `X-CSRF-Token`, полученным при login. Для обоих
требуется effective permission `conversation.reply` и строка
`channel_memberships` текущего пользователя для Conversation Channel с
`can_reply=true`. Assignment не участвует в авторизации.

- `PATCH /api/v1/conversations/{id}/status`: тело содержит `expected_version`,
  `status` (`open`, `pending`, `resolved`, `snoozed`) и `snoozed_until` для
  snooze.
- `PATCH /api/v1/conversations/{id}/priority`: тело содержит
  `expected_version` и `priority` (`low`, `normal`, `high`, `urgent`).

Оба ответа `200` возвращают Conversation с увеличенным `version`. API
возвращает `400` для невалидного запроса, `401` для отсутствующей/недействующей
session, `403` для CSRF/RBAC/membership, `404` для отсутствующей Conversation и
`409` для stale version либо недопустимого status transition. Membership
блокируется в той же PostgreSQL transaction, что и Conversation write и outbox
event, поэтому запрет не создаёт частичных изменений.
