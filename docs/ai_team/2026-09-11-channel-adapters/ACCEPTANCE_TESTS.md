# Acceptance tests: канальные adapters

Статус: `ready_for_review`. Все сценарии должны стать RED до реализации соответствующего модуля.

| ID | Given | When | Then | Уровень |
|---|---|---|---|---|
| AT-CHA-001 | Administrator с `channel.manage` и valid type-specific config | Создаёт channel | 201 returns safe summary; secret/ciphertext отсутствуют в JSON, logs и audit/outbox payload | HTTP + integration |
| AT-CHA-002 | Agent без `channel.manage` | Прямой POST/PATCH/validate/enable channel route | 403; ничего не меняется | HTTP |
| AT-CHA-003 | New channel с required secret | Validate fails/provider rejects | Channel не active; safe code shown; secret не отражён | integration + UI |
| AT-CHA-004 | Validated channel | Enable | provider registration executes with bounded context; active only after success | adapter fake + integration |
| AT-CHA-005 | Active channel and a verified inbound event | Same event reaches webhook twice/concurrently | one normalized Message/outbox fact; canonical duplicate returned | integration/race |
| AT-CHA-006 | Disabled/degraded channel | Webhook arrives | no new core facts; safe rejection/observability only | integration |
| AT-CHA-007 | Agent queues an outbound message | Worker gets provider accepted response | message is marked sent with provider ID; no provider DTO leaks to core | integration |
| AT-CHA-008 | Worker times out after outbound request | no definitive provider response | no second external send; message is reconcilable/unknown until status check or callback | adapter fake |
| AT-CHA-009 | Definitive provider error | Worker dispatches queued message | `MarkFailed` receives stable code; content/credentials absent from logs | integration |
| AT-CHA-010 | Administrator opens each of five type routes | Navigates with keyboard, enters invalid field, tests config | distinct labelled wizard and safe status/error state; no raw JSON/secrets | web accessibility |
| AT-CHA-011 | Administrator creates `telegram_bot` or `telegram_user` | Configures the adapter | Distinct immutable type, route and credential schema; bot never persists phone/session and user mode never accepts bot token | HTTP + UI |
| AT-CHA-013 | Telegram user mode needs reauthentication/2FA | Administrator starts the explicit re-login lifecycle | session material remains encrypted/non-returnable; no phone/2FA secret reaches URL, logs or browser storage | security integration + UI |
| AT-CHA-012 | Provider spoofed/unsigned callback | Calls webhook route | rejected before normalization; no facts/outbox effects | security integration |

Provider-specific additions before each implementation: official contract fixtures for Telegram chosen mode, Carrot quest endpoint/event model, OMNI send/status/callback, MAX subscription/message event, VK Callback confirmation/message event; contract tests must include signature/auth, rate limit, success, permanent failure, ambiguous timeout and duplicate event.
