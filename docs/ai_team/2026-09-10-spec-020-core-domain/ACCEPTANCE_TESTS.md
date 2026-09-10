# Контракт приёмки: SPEC-020 Core Domain, первая vertical

Статус: `ready_for_review`.  
Дата: 2026-09-10. Владелец: QA-инженер.  
Предусловие: этот контракт создан **до production-кода**. Каждый указанный RED
тест обязан воспроизводимо падать в текущем checkout по причине отсутствия
требуемого поведения, а не вследствие недоступной инфраструктуры. Реализация
может начинаться только после фиксации RED evidence.

## 1. Scope и источники

Контракт конкретизирует первую вертикаль из `SPEC-020` и минимальную,
необходимую для неё часть `SPEC-030`: provider-neutral core, PostgreSQL source
of truth, transactional outbox и durable evidence при остановленном dispatcher.
Он основан на `RND.md`, `BUSINESS_ANALYSIS.md`, `DECISIONS.md`,
`ARCHITECTURE.md`, `spec/020-core-domain.md` и `spec/030-events-jobs-outbox.md`.

Утверждённые решения, обязательные для всех сценариев:

| Решение | Проверяемое следствие |
|---|---|
| DEC-020-01 | `assignee_id` не является ACL. Active agent с глобальным permission и Channel membership может читать и отвечать независимо от назначения; без membership доступа нет. |
| DEC-020-02 | `Resolve` принимает `expected_version`; stale command возвращает `version_conflict`, а inbound после блокировки оставляет Conversation `open`. |
| DEC-020-03 | В БД хранится `timestamptz`, time source — PostgreSQL/server mutation time; API возвращает instant, UI отображает его в `Europe/Moscow`. Provider time не управляет waiting/response timestamps. |
| DEC-020-04 | `sent -> failed` восстанавливает waiting только для сообщения, которое закрыло текущий episode, и создаёт corrective event. |

### Вне первого инкремента

Следующие требования SPEC-020/030 остаются явными MVP omissions и не могут
объявляться реализованными по результатам этого набора: provider webhooks и
raw `inbound_events`, provider send/reconciliation, SMTP/Telegram/VK/MAX
adapters, attachments, inbox UI/listing, queues/routing, scheduled snooze
wakeup, dispatcher/jobs/leases/retries/dead letters/handler receipts, external
exactly-once delivery, SLA engine и analytics. Для них здесь фиксируется лишь
совместимая граница: outbox event остаётся durable при выключенном dispatcher.

## 2. Общая тестовая среда и evidence

| Объект | Требование |
|---|---|
| БД | Изолированная PostgreSQL test database. Redis не используется как durable evidence. |
| Схема | Миграция запускается с пустой БД и вперёд с foundation/auth schema version 2; migration checksum проверяется existing runner. |
| Время | Тест фиксирует/считывает `mutation_time` из PostgreSQL и сравнивает instants. Отображение проверяет `Europe/Moscow`, включая offset; не сравнивает строку локального времени с UTC literal. |
| Конкурентность | Для race используются минимум два независимых DB connection/transaction и барьер до contested write; goroutine без отдельных connections не является достаточным evidence. |
| Assertions | После каждого отказа проверяются rows core и `outbox_events`, conversation version и отсутствие orphan Contact. |
| RED mapping | Каждый ID ниже получает именованный test до implementation, например `TestReceiveInbound_*`, `TestQueueOutbound_*`, `TestConversation_*`, `TestCoreMigration_*`. В PR сохраняются команда, exit code и ожидаемый отсутствующий symbol/поведение. |

## 3. Acceptance scenarios

### A. Schema, domain boundaries и базовая целостность

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-001 | Пустая PostgreSQL DB и migration runner. | Выполняется `migrate up`, затем проверяется schema. | Созданы enums/tables/indexes из SPEC-020 и минимальный `outbox_events`; UUIDs, display-only Conversation number, partial uniques, FKs, `version >= 1`, snooze consistency и checksum migration присутствуют. | `TestCoreMigrationEmptyDatabase`; до миграции version/schema assertions fail. |
| AT-020-002 | DB на schema version 2 после SPEC-010. | Выполняется новая forward migration дважды. | Первый запуск создаёт core/outbox schema; повтор безопасен через schema migration record; applied migration 1/2 не изменены. | `TestCoreMigrationFromVersion2`, `TestCoreMigrationChecksum`; до реализации migration unknown/missing. |
| AT-020-003 | Два Channels и ContactIdentity одного Channel. | Создаётся Conversation или Message с channel другого Channel. | Запрос отвергнут stable error `contact_identity_channel_mismatch`/`conversation_channel_mismatch`; не создаются Message/outbox rows. | PostgreSQL integration `TestChannelCrossTableIntegrity`; RED — service/repository отсутствует. |
| AT-020-004 | Один Contact уже имеет identity в Channel A; external user ID совпадает в Channel B. | Создаётся identity в Channel B и второй identity с тем же ID в Channel A. | Разные Channels допускают одинаковый external ID; повтор в том же Channel отвергнут unique constraint/typed duplicate result. Email/phone не используются как merge key. | `TestContactIdentityChannelScopedUniqueness`. |
| AT-020-005 | Core packages собраны. | CI выполняет import-boundary check. | `internal/channels/core`, contacts, conversations, messages, events и core app не импортируют provider SDK/packages Telegram/VK/MAX/email; normalized contract не содержит provider model. | `TestCoreHasNoProviderImports` / `go list -deps`; RED until new packages/contracts exist. |

### B. Channel ACL и командная видимость

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-010 | Active agent A имеет global `conversation.read`/`conversation.reply`, `can_read=true`, `can_reply=true` membership Channel C; Conversation C назначен agent B. | A получает Conversation и создаёт outbound reply. | Обе операции разрешены. `assignee_id=B` не попадает в authorization predicate и не фильтрует историю/ответ. | `TestChannelMemberCanReadAndReplyUnassignedConversation`. |
| AT-020-011 | Active agent имеет global permission, но отсутствует Channel membership или capability `can_read`/`can_reply=false`. | Он читает или отвечает в Conversation Channel C. | HTTP/domain result `forbidden`; нет Message, version increment и outbox event. | `TestChannelMembershipCapabilityDenied`. |
| AT-020-012 | Membership существовал в начале конкурирующей operator mutation. | До locked write membership отзывается в отдельной transaction. | Write transaction повторно проверяет/locks membership и завершает `forbidden`; частичных core/outbox данных нет. | Two-connection `TestMembershipRevocationWinsBeforeMutation`. |

### C. ReceiveInbound, identity и conversation races

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-020 | Active enabled Channel и valid `NormalizedInboundMessage` с новой `(channel_id, external_user_id, external_thread_id, external_message_id)`. | `ReceiveInbound` вызывается один раз. | Созданы один Contact, ContactIdentity, current Conversation `open`, immutable incoming Message `received`, waiting episode и required outbox events; result содержит canonical IDs. | `TestReceiveInboundCreatesCoreFactsAndOutbox`. |
| AT-020-021 | Две независимые transactions одновременно получают первое inbound от одной identity. | Обе проходят барьер перед identity creation. | Существует одна ContactIdentity и один связанный Contact; проигравшая transaction rereads winner/retries as designed; orphan Contact отсутствует. | Two-connection `TestReceiveInboundConcurrentFirstIdentity`. |
| AT-020-022 | Две independent transactions одновременно получают inbound одной identity/thread при отсутствии current Conversation, но с разными external message IDs. | Обе проходят барьер до current Conversation insert. | Создана одна current Conversation для `(channel, thread, identity)`; оба distinct Messages принадлежат ей, её state/version корректны; partial unique/advisory lock предотвращают second current row. | `TestReceiveInboundConcurrentCurrentConversationCreation`. |
| AT-020-023 | Conversation уже существует и одна customer waiting episode начата. | Два разных inbound приходят параллельно. | `waiting_since` равен earliest committed first message mutation time и не переносится на второй; `last_activity_at` не уменьшается; version increments атомарно. | `TestReceiveInboundConcurrentWaitingEpisode`. |
| AT-020-024 | Один и тот же `(channel_id, external_message_id)` доставляется 10 раз последовательно и затем конкурентно. | Вызывается `ReceiveInbound`. | Persisted ровно один Message, один соответствующий набор core events и нет лишнего version increment; каждый retry возвращает canonical duplicate-safe result, не server error. | `TestReceiveInboundDuplicateSequential`, `TestReceiveInboundDuplicateRace`. |
| AT-020-025 | Channel `disabled`, `enabled=false`, либо status не `active`. | Приходит valid normalized inbound. | Возвращается `channel_disabled`/typed error; Contact, identity, Conversation, Message и outbox не создаются. | `TestReceiveInboundRejectsInactiveChannel`. |

### D. Conversation state, optimistic conflict и read state

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-030 | Conversation каждого status (`open`, `pending`, `snoozed`, `resolved`). | Customer inbound received. | Итог `open`; для `snoozed` очищен `snoozed_until`, для `resolved` очищен `resolved_at`; former pending/open timestamps remain semantically valid; emitted `conversation.reopened` только для snoozed/resolved. | Table test `TestInboundReopensConversationStates`. |
| AT-020-031 | Authorized operator и state transition matrix SPEC-020. | Выполняются все allowed и forbidden explicit transitions. | Allowed transitions mutate exactly once/version; forbidden return `invalid_conversation_transition`, leave state/version/outbox unchanged. Priority changes independently and does not alter status. | `TestConversationStatusTransitionTable`, `TestPriorityDoesNotChangeStatus`. |
| AT-020-032 | Operator прочитал Conversation at version N; конкурентный inbound получает Conversation lock и commits version N+1. | Operator invokes Resolve with `expected_version=N`. | Result `version_conflict` (HTTP 409 where exposed), no resolve mutation/event; UI-facing result safely includes current version or forces reread. Conversation remains `open`. | Two-connection `TestResolveStaleVersionAfterInbound`. |
| AT-020-033 | Resolve command and inbound start concurrently from `open` Conversation. | Они contend for Conversation lock. | После both completion final status `open`; inbound Message saved exactly once; command either resolves then inbound reopens, or returns `version_conflict`; new customer message never remains resolved. | `TestResolveAndInboundRaceLeavesOpen`. |
| AT-020-034 | Users A and B have valid read access to one Conversation with two ordered Messages sharing/similar timestamps. | A marks read through newest `(created_at,id)` boundary. | A read state updates; B remains unread; unread query/order uses `(created_at,id)`, never timestamp alone. | `TestConversationReadStateIsPerUserAndOrdered`. |

### E. Waiting episode, response lifecycle и Moscow time

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-040 | Active Conversation has no waiting episode. | First customer inbound commits at DB mutation time T1. | `waiting_since=T1`, `last_inbound_at` and `last_activity_at` do not precede T1; stored type is `timestamptz`. A provider `ExternalCreatedAt` different from T1 does not control these values. | `TestWaitingStartsAtDatabaseMutationTime`. |
| AT-020-041 | Customer inbound commits at T1; more inbound commits at T2/T3 before a sent human response. | State is read after T3. | `waiting_since` remains T1; `last_inbound_at`/activity advance without moving backwards. | Golden `TestWaitingDoesNotMoveForAdditionalInbound`. |
| AT-020-042 | Conversation is waiting. | Agent queues a reply, then it fails; AI, workflow and system outgoing records are also created. | `waiting_since` and `first_response_at` stay unchanged for queued, failed, AI/workflow/system messages. | Table/golden `TestNonHumanOrUnsentReplyDoesNotClearWaiting`. |
| AT-020-043 | Conversation waits since T1; agent outbound reaches `sent` at DB mutation time T2. | `MarkSent` commits. | `waiting_since=NULL`; `first_response_at=T2` once; `waiting_closed_by_message_id` equals this Message; outbound/activity timestamps do not move backwards. A second sent agent reply does not overwrite `first_response_at`. | `TestFirstSentAgentReplyClosesWaiting`, `TestSecondSentReplyKeepsFirstResponse`. |
| AT-020-044 | Message M closed current waiting episode at T2. | Provider makes definitive `sent -> failed` correction before a newer customer waiting episode. | Waiting is restored to the correct prior episode start, closure reference cleared/updated according to model, and one `message.delivery_corrected` event has `waiting_restored=true`. | `TestLateSentFailureRestoresCoveredEpisode`. |
| AT-020-045 | M previously closed an episode, then later inbound starts a newer waiting episode or another sent reply closes it. | M changes `sent -> failed`. | Delivery status changes, but current `waiting_since`, `first_response_at` and newer closure reference are not overwritten; corrective event records `waiting_restored=false`. | `TestLateSentFailureCannotRewriteNewEpisode`. |
| AT-020-046 | A persisted API DTO includes lifecycle instants. | Service/API serializes values and UI formatter receives them. | API preserves timezone-aware instant; formatter displays expected `Europe/Moscow` date/time. No client time nor provider timestamp is accepted as authoritative mutation time. | `TestLifecycleTimeSerializedAndRenderedMSK`; backend and frontend test if endpoint/UI lands in this vertical. |

### F. Outbound idempotency и message immutability

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-050 | Authorized Channel member queues outbound request R with user-scoped key K. | Same user repeats exactly R with K. | Returns the original Message ID/status, does not create a second Message/outbox event, and creates one durable idempotency record. | `TestQueueOutboundSameKeySameRequest`. |
| AT-020-051 | Same user has key K recorded for request hash R. | Same K is used with changed body, reply target or Conversation. | `idempotency_conflict` / HTTP 409; original data stays unchanged; no second Message/event. | `TestQueueOutboundSameKeyDifferentRequestConflict`. |
| AT-020-052 | Two independent connections submit R with same user/K concurrently. | Both cross a barrier before idempotency insert. | One creates the Message; the other returns its canonical result; database contains one Message/idempotency row/outbox intent. | `TestQueueOutboundIdempotencyRace`. |
| AT-020-053 | A Message exists. | Code/repository tries to change immutable identity/content fields, then changes allowed delivery metadata via valid transitions. | Immutable update rejected; valid status transition changes only operational fields and emits matching event. Invalid message state transition is rejected without partial changes. | `TestMessageImmutability`, `TestMessageStatusTransitionTable`. |

### G. Transactional outbox, failure injection и dispatcher-off durability

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-060 | ReceiveInbound reaches a controllable failpoint immediately after Message insert and before Conversation mutation. | Failpoint returns error. | Transaction rolls back: no Message, Contact/Identity/Conversation created only by this call, version or outbox row persists. | `TestReceiveInboundRollbackAfterMessageInsert`. |
| AT-020-061 | ReceiveInbound reaches failpoint after Conversation mutation and before `OutboxWriter.Append`, then separately during Append. | Each failpoint returns error. | No partial core state and no outbox event is visible after rollback. | `TestReceiveInboundRollbackBeforeAndDuringOutboxAppend`. |
| AT-020-062 | A core mutation successfully commits while dispatcher/worker is not running. | DB is inspected after API/service returns success. | Required `outbox_events` row is durable, `dispatched_at IS NULL`, event payload has IDs/classification/version but no message body/HTML/identity/email/phone/header/credential/session data. No claim of external delivery is made. | `TestCommittedMutationLeavesUndispatchedRedactedOutboxEvent`. |
| AT-020-063 | Runtime composition is built. | It attempts to use no-op/in-memory/Redis-only OutboxWriter for a core mutation. | Construction/startup fails before accepting traffic, or integration proves actual PostgreSQL outbox writer is used. | `TestRuntimeRejectsNoopOutboxWriter`. |
| AT-020-064 | Domain event payload schema v1 exists. | Consumer-facing payload is decoded and a new required field is proposed later. | Existing v1 fields remain explicit/versioned; a change requiring incompatible shape requires new payload version. | Contract `TestCoreOutboxPayloadV1`; later version test when changed. |

### H. HTTP contract, observability, security и privacy

| ID | Given | When | Then | RED mapping / evidence |
|---|---|---|---|---|
| AT-020-070 | Public operator endpoints for core are exposed in this vertical. | OpenAPI validation and request/response contract tests run. | `api/openapi.yaml` describes auth, Channel-scoped endpoints, required `expected_version`, idempotency key and stable 400/403/409/503 error envelopes; invalid/stale/forbidden outcomes match document. If endpoints are intentionally deferred, no undocumented public core endpoint is exposed and this scenario is deferred with the endpoint task. | `TestCoreOpenAPIContract` plus validator and negative-control invalid spec. |
| AT-020-071 | Successful inbound, duplicate inbound, status transition and outbound send-state transition occur. | `/metrics` is scraped. | Exposes finite-label metrics from SPEC-020: contacts created, identity conflicts, conversations created, status changes `{from,to}`, messages created `{direction,actor_type}`, message status changes `{from,to}`, duplicates, waiting started/cleared; minimal outbox created/undispatched evidence from SPEC-030 is present. | `TestCoreMetrics`; labels must not include user IDs, message text, email, external IDs or error bodies. |
| AT-020-072 | Test sends sentinel message text/HTML, external identity, email/phone and fake provider credential/header through core. | Structured logs, traces and outbox payload are collected. | Sentinels do not occur in logs, trace attributes or outbox payload; logs use opaque IDs/event type/stable code only. Normal operator DTO excludes Channel credentials. | `TestCorePrivacyRedaction`; fail closed on sentinel search. |
| AT-020-073 | Input has invalid enum/empty external IDs/oversize or malformed fields, or authenticated user is disabled. | Boundary invokes core command. | `validation_failed`/`forbidden` with no mutation; no provider raw model crosses into core. Every external call remains absent in this vertical, so no timeout-less provider request exists. | `TestCoreInputValidationAndDisabledPrincipal`. |

## 4. RED-to-GREEN execution order

1. Migration/domain constraint and provider-boundary tests: AT-020-001…005.
2. ReceiveInbound happy path and atomicity: AT-020-020, 025, 060…064.
3. Locks, duplicates and membership concurrency: AT-020-010…012, 021…024, 032…033.
4. State/waiting/delivery correction: AT-020-030…046.
5. Outbound idempotency, read state and immutable records: AT-020-034, 050…053.
6. HTTP/metrics/privacy contract for each public/runtime surface: AT-020-070…073.

Before each step, preserve the failing command and assertion in the task's
implementation evidence. GREEN requires the named test and its nearest
regression group; final verification includes PostgreSQL integration tests,
`go test -race ./...`, `go vet ./...`, OpenAPI validation with negative control,
coverage gate, independent review and QA retest.

## 5. Exit criteria for this vertical

The vertical can move to review only when all applicable scenarios are GREEN,
MVP omissions are still documented as deferred, and evidence proves real
PostgreSQL transaction/outbox behaviour rather than mocks. It cannot claim
completion of full SPEC-030, provider integration, external delivery or inbox
UI.
