# Acceptance tests: operator transitions

Статус: `ready_for_review`. Эти сценарии должны быть RED до production-кода и выполняться на remote dev PostgreSQL.

| ID | Given | When | Then |
|---|---|---|---|
| AT-TR-001 | Conversation каждого status | Выполняются все разрешённые переходы | status/markers меняются, version увеличивается на 1, один нормативный outbox event committed; matrix проверяет точный event type, включая `conversation.reopened` |
| AT-TR-002 | Conversation каждого status | Выполняется запрещённый переход | `invalid_conversation_transition`; status/version/outbox не меняются |
| AT-TR-003 | `open`/`pending` version N | Resolve с `expected_version=N` | `resolved_at` использует DB time, `snoozed_until=NULL`, event содержит version N+1 |
| AT-TR-004 | Conversation version N, concurrent inbound commits N+1 | Resolve с N | `version_conflict`, inbound state `open` остаётся, нет resolve event |
| AT-TR-005 | Allowed source status | Snooze без deadline, с прошлым deadline, затем с будущим deadline | первые два validation error без mutation; valid call ставит `snoozed` и deadline |
| AT-TR-006 | Conversation любого status | Устанавливается каждая valid priority | Только priority/version/event меняются; status и lifecycle markers прежние |
| AT-TR-007 | Failpoint outbox append | Valid transition | Transaction rollback: нет status/version/marker mutation и нет event |
| AT-TR-008 | Две competing operator commands с one `expected_version` | Обе запускаются concurrent | Ровно одна commit; другая получает `version_conflict`; один event |

GREEN: targeted + full remote `-race` integration suites, coverage >= current baseline, `go vet`, `gofmt`, CI deploy; затем independent review и QA.
