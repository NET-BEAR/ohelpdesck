# Исследование SPEC-020 и зависимого SPEC-030

Статус: `approved`.  
Дата: 2026-09-10. Владелец: Бизнес-аналитик.  
Граница: анализ `spec/020-core-domain.md`, необходимой части `spec/030-events-jobs-outbox.md`, текущего кода и утверждённых task artifacts. Production-код, миграции и `PLANS.md` не изменялись.

## Источники и уровень доказательств

| Источник | Что подтверждает | Ограничение |
|---|---|---|
| `spec/020-core-domain.md` | P0-модель core, SQL-инварианты, state machines, 22 AC и обязательные тесты | Это target contract, не доказательство работающего поведения |
| `spec/030-events-jobs-outbox.md` | durable outbox, jobs, leases, receipts и crash guarantees, нужные core | Полный worker/ingress не является частью минимальной core-вертикали |
| `docs/ai_team/2026-09-10-project-foundation/{RND,ARCHITECTURE,BUSINESS_ANALYSIS}.md` | уже выявленные GAP-01, 03–11 и решение о едином durability gate | Открытые product decisions не считаются утверждёнными |
| Текущий checkout | Есть foundation и `internal/auth`; permission catalog уже содержит conversation permissions | Нет `internal/channels/core`, `contacts`, `conversations`, `messages`, `events`, доменных миграций и integration tests core/outbox |

Memory MCP, `.env`, удалённый контур, Jira и Confluence не использовались. Файл роли `docs/agents/roles/business-analyst.md` в checkout не обнаружен; анализ выполнен по входным task artifacts и проектным правилам `AGENTS.md`.

## Подтверждённая целевая модель

Core остаётся provider-neutral. Он не импортирует Telegram/VK/MAX/Email SDK и не принимает их payload-модели. Адаптеры позднее нормализуют входящие события до `NormalizedInboundMessage`; core возвращает provider-neutral `OutboundMessage` для последующей отправки.

| Сущность | Роль и ключевые данные | Состояние / ограничения |
|---|---|---|
| Channel | Настроенная точка коммуникации; тип, статус, включённость, внешний account ID | тип: telegram/vk/max/email; статус: active/degraded/reauthorization_required/disabled/error; credentials доступны только adapter/config layer |
| Contact | Каноническая внутренняя карточка клиента | `internal_customer_id` уникален, когда задан; email/phone не глобально уникальны |
| ContactIdentity | Идентичность Contact внутри Channel | уникальна по `(channel_id, external_user_id)`; связывает одну identity с одним Contact |
| Conversation | Операционный thread контакта по каналу | UUID и display-only возрастающий number; status, priority, assignee, waiting/response/activity timestamps, version |
| Message | Неизменяемый факт коммуникации и изменяемые delivery-данные | внешний message ID уникален в Channel; content/author/channel/thread immutable, status/receipt metadata mutable |
| ConversationReadState | Персональная отметка прочтения оператора | ключ `(conversation_id, user_id)`; не влияет на других операторов |

## Бизнес-правила и зависимости

1. При входящем сообщении сначала разрешается identity по `(channel_id, external_user_id)`. При отсутствии создаются Contact и ContactIdentity; гонка должна закончиться одной identity и повторным чтением победившей записи.
2. Current conversation выбирается по `(channel_id, external_thread_id, contact_identity_id)`. Отсутствующая создаётся как `open`; `pending`, `snoozed` и `resolved` после сообщения клиента переходят в `open`. Для `snoozed` очищается дата пробуждения, для `resolved` — дата resolution.
3. Входящее сообщение, изменение Conversation и append доменных событий образуют одну PostgreSQL transaction. Нельзя сохранить Message без требуемого изменения conversation или outbox event.
4. `waiting_since` устанавливается временем первого неотвеченного customer message и не сдвигается следующим customer message. Его очищает только sent human reply (`actor_type=agent`); queued, failed, AI, workflow и system сообщения не отвечают клиенту. `first_response_at` устанавливается ровно один раз.
5. У operator status transitions ограничены графом: open→pending/resolved/snoozed; pending→open/resolved/snoozed; snoozed→open/resolved; resolved→open. Priority меняется независимо от статуса. Snooze wakeup идемпотентен и требует scheduled job из SPEC-030.
6. Outgoing API требует ключ идемпотентности, scoped на user: повтор того же request возвращает исходный Message, другой request с тем же ключом — conflict.
7. Каждая business-relevant mutation увеличивает Conversation.version. Read-state использует согласованный порядок `(created_at, id)`, а не только время.
8. Outbox writer из SPEC-030 пишется в ту же transaction, что и core mutation. В runtime запрещён no-op writer: dispatcher создаёт durable job на handler и фиксирует `dispatched_at` только после успешного fan-out.

## Минимальная целевая вертикаль

Ценность первого инкремента: система может принять уже нормализованное входящее сообщение для заранее созданного Channel, идемпотентно создать/найти контакт, identity и conversation, зафиксировать waiting episode и atomically записать core events в PostgreSQL outbox. Эта вертикаль не открывает provider webhook, не отправляет сообщения внешнему провайдеру и не запускает полный worker.

| Включить сейчас | Почему достаточно | Отложить |
|---|---|---|
| Core value objects/enums, schema Channel/Contact/Identity/Conversation/Message/ReadState/idempotency | Формирует нейтральный durable source of truth | UI inbox/listing из SPEC-090, очереди SPEC-100 |
| `ReceiveInbound` с locks, duplicate-safe message insert, waiting/status rules | Закрывает центральный бизнес-цикл и основные races | HTTP webhook verification и provider parsing |
| Minimal transactional `OutboxWriter`, `outbox_events`, Event contract | Доказывает FR-CORE-003/004 и DATA-EVT-003 | Inbound event store, queue, dispatcher, leases, retry, receipts |
| Conversation status/priority/read commands с RBAC boundary | Делает core пригодным для следующей операторской вертикали | Assignee/resource policy до решения ниже |
| PostgreSQL integration/failure tests, metrics without text bodies | Проверяет важные ограничения до роста функциональности | Реальные metrics dashboard/alert rollout |

В этой вертикали создание outbound Message допускается только как domain operation `queued` с durable intent; внешний send handler должен появиться вместе с полным SPEC-030 и channel contract. `MarkSent` нельзя считать доказанным без provider-specific reconciliation.

## Набросок acceptance в форме Given/When/Then

| ID | Given | When | Then |
|---|---|---|---|
| BA-020-01 | Есть активный Channel и normalized inbound с новой external identity | `ReceiveInbound` обработан дважды, в том числе параллельно | Есть один ContactIdentity, один Message для external message ID и одна корректная Conversation; повтор даёт duplicate-safe результат |
| BA-020-02 | Conversation `pending`, `snoozed` или `resolved` и есть `waiting_since`/служебные timestamps | Приходит customer message | Conversation становится `open`; snooze/resolution markers очищаются; новый inbound не остаётся скрытым |
| BA-020-03 | В 10:00 создан inbound Message, затем в 10:05 второй; agent reply в 10:10 `queued`, затем `failed` | В 10:12 этот reply либо иной agent reply получает `sent` | До `sent` `waiting_since=10:00`; после первого successful human response оно очищено, а `first_response_at=10:12` и далее не перезаписывается |
| BA-020-04 | При сохранении inbound операции инъецирована ошибка после Message либо Conversation, но до outbox commit | Transaction завершается ошибкой | Нет частичных Message/Conversation/outbox rows |
| BA-020-05 | Один agent уже создал outbound Message с idempotency key и request hash | Он повторяет тот же request, затем другой body с тем же key | Первый вызов возвращает тот же Message; второй получает `idempotency_conflict`; второй Message не создаётся |
| BA-020-06 | Две transaction одновременно получают inbound для одного identity/thread, а оператор пытается resolve | Transactions получают locks и переоценивают состояние | identity не дублируется; конечный status соответствует согласованному below rule, conversation version атомарно возрастает |
| BA-020-07 | Domain mutation успешно коммитится, но dispatcher/worker остановлен | Проверяется БД | Undispatched outbox event сохранён и может быть обработан позднее; не заявляется exactly-once external delivery |

## Утверждённые решения перед реализацией

| ID | Варианты | Последствия | Рекомендация |
|---|---|---|---|
| DEC-020-01: resource policy до queues | A: agent видит/меняет только assigned conversations; B: доступен любой conversation в allowed channel; C: доступ ко всем | A безопаснее, но нужна первичная assignment semantics; B требует Channel ACL, которых spec пока не задаёт; C делает RBAC слишком широким | **Утверждён B с Channel ACL:** assignment не является ACL; команда видит и подхватывает диалоги, а единый account сохраняет персональную подпись в теле email |
| DEC-020-02: resolve vs inbound race | A: last serialized command wins; B: operator resolve требует expected version и получает conflict при inbound; C: inbound всегда побеждает специальным приоритетом | A способен оставить новый inbound resolved; C усложняет общий lock semantics; B прозрачен для UI и сохраняет доказуемость | **Утверждён B** |
| DEC-020-03: clock и coverage human reply | A: provider timestamps; B: DB/server commit time; C: комбинированное правило | A неуниформен и допускает недоверенные/опоздавшие часы; C сложнее тестировать | **Утверждён B:** timezone-aware storage, API/UI `Europe/Moscow` |
| DEC-020-04: `sent → failed` после нового inbound | A: не корректировать waiting; B: восстановить только если current episode всё ещё covered этим Message; C: всегда восстановить | A искажает SLA, C может стереть начало нового episode | **Утверждён B** |
| DEC-020-05: unread ordering | A: `(created_at,id)`; B: per-conversation ordinal; C: только timestamp | C противоречит spec; A проще, но late commit edge case остаётся; B даёт строгий курсор, добавляет schema/locking | **A** в MVP с явным documented late-commit behaviour и regression test; перейти к B, если операторский UI требует strict read-after-write |
| DEC-020-06: minimum outbox scope | A: fake/no-op runtime writer; B: minimal real `outbox_events`; C: весь SPEC-030 first | A нарушает atomicity; C откладывает core ценность на worker scope | **B**, затем общий core/outbox integration gate и полный SPEC-030 |

## Риски и следующий этап

Главные риски: неутверждённая visibility policy, неоднозначность sent→failed, несериализованный creation current conversation при отсутствии row, и ошибочная подмена durable outbox Redis/no-op реализацией. Риск внешней доставки остаётся вне этой вертикали: только канал сможет доказать provider idempotency/reconciliation после передачи HTTP request.

Следующая роль: Архитектор — подготовить `ARCHITECTURE.md` и уточнить transaction/locking contracts вместе с минимальным SPEC-030 outbox slice. После решения DEC-020-01..04 — QA/БА формирует полный `ACCEPTANCE_TESTS.md`; затем Разработчик начинает RED.
