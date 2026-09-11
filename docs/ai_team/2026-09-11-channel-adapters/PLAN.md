# Канальные адаптеры: Telegram, Carrot quest, OMNI, MAX и VK

Статус задачи: `in_implementation`. Дата: 2026-09-11. Владелец: оркестратор.

## Цель и scope

Добавить к модульному монолиту шесть независимых adapter-модулей: Telegram Bot API через GoTd, Telegram MTProto user-account через GoTd, Carrot quest API, MTS OmniChannel HTTP SMS gateway, MAX Bot API и VK Messages API. Для каждого канала administrator с `channel.manage` создаёт, проверяет, включает, отключает и видит безопасный status summary отдельного Channel в UI. Все каналы двусторонние: inbound события нормализуются и передаются в `core.ReceiveInbound`; исходящие берутся worker из durable outbox, отправляются с provider-specific idempotency/reconciliation и меняют lifecycle через `core.OutboundService`.

Вне scope до решения: реальные credentials, создание/изменение bot/provider account, автоматические рассылки, перенос историй, content/attachment support сверх текста, отправка SMS в production и обработка персональных данных за пределами текущих contract boundaries.

## Выбранные компетенции

| Роль | Компетенция | Выбранный skill/источник | Статус | Причина |
|---|---|---|---|---|
| Оркестратор | Docs-driven delivery | `docs-driven-delivery` | local_approved | Обязательные документы, RED/GREEN/review/QA gates |
| Оркестратор | Task lifecycle | `task-artifacts` | local_approved | Реестр, состояния и evidence |
| Архитектор | Provider-neutral contracts | built-in code inspection + official provider docs | built_in | В реестре нет доступного проверенного `api-design`; внешние skills не нужны |
| Документ | Прочитать приложенный контракт | `pdf` | built_in | Визуально проверен приложенный PDF |

Отклонены новые provider SDK/skills и plugins: подключение не требуется для проектирования; runtime dependency выбирается после безопасного режима Telegram и фиксируется в отдельном dependency rationale.

## Зависимости и gates

1. Выполнено: core inbound/outbound contracts, Channel ACL и outbox lifecycle существуют в checkout.
2. Решено: оба Telegram режима — самостоятельные adapter/channel types; все каналы поддерживают inbound и outbound. Secret store — AES-256-GCM ciphertext in PostgreSQL with deployment-injected 32-byte key. Реальные sandbox credentials/URLs и callback endpoints потребуются лишь для provider-contract/QA. Credentials не запрашиваются и не будут храниться в git/UI.
3. До включения production Channel: операторские API для channel administration, migration, OpenAPI, RED acceptance tests, implementation, independent review и QA.
4. Каждая внешняя HTTP операция имеет `context`/timeout; webhook/callback проходит provider authentication, replay protection и idempotency; provider DTO не попадают в `core`.

## Маршрут после решения

1. Уточнить `RND.md`, `BUSINESS_ANALYSIS.md`, `ARCHITECTURE.md`, `UX_DESIGN.md`, `ACCEPTANCE_TESTS.md` и записать `DECISIONS.md`.
2. Написать RED acceptance/integration tests для admin API и одного выбранного provider adapter, затем минимальную реализацию. Открывать последующие adapters по одному после GREEN; один writer на набор файлов.
3. Обновить `api/openapi.yaml`, выполнить unit/integration/web checks и coverage gate.
4. Передать независимому Reviewer, затем QA; production activation остаётся отдельным согласованным шагом.

## Обратная связь пользователя и impact analysis

Пользователь подтвердил 2026-09-11: (1) нужны оба варианта Telegram как разные channels/adapters; (2) нужен двусторонний режим для всех каналов. Класс: **фундаментальное изменение** относительно одной Telegram реализации и inbound-only варианта. Минимальная точка возврата: `BUSINESS_ANALYSIS.md`; downstream: `ARCHITECTURE.md`, `UX_DESIGN.md`, `ACCEPTANCE_TESTS.md`, затем Developer → независимый Reviewer → QA. Предыдущие draft-формулировки заменены в текущих версиях артефактов; code ещё не было, поэтому code rework не нужен. Использован `feedback-impact-analysis` (local_approved).

## Решённые и открытые решения

| ID | Вопрос | Варианты | Рекомендация |
|---|---|---|---|
| DEC-CHA-01 | Telegram transport / identity | **Решено:** A и B. `telegram_bot` использует GoTd Bot API; `telegram_user` использует GoTd MTProto с отдельными session/2FA controls. Это разные immutable Channel types, UI routes, credential schemas и adapters. |
| DEC-CHA-02 | Где хранить provider credentials и Telegram user session? | A: application-level AES-256-GCM encrypted blob in PostgreSQL, data-encryption key injected by deployment environment; B: external KMS/secret manager; C: plaintext or reversible config JSON | **Рекомендация: A** для текущего локального модульного монолита: database остаётся durable storage, key не хранится в PostgreSQL/git/UI и rotation versioned. B лучше для managed production but requires approved provider/plugin. C запрещён. |

## Артефакты

| Файл | Статус | Назначение |
|---|---|---|
| `RND.md` | ready_for_review | Факты внешних документов и риски |
| `BUSINESS_ANALYSIS.md` | ready_for_review | Сценарии, границы и data rules |
| `ARCHITECTURE.md` | ready_for_review | Модули, contracts, security и worker flow |
| `UX_DESIGN.md` | ready_for_review | Отдельная admin-настройка каждого канала |
| `ACCEPTANCE_TESTS.md` | ready_for_review | Given/When/Then до кода |
| `DECISIONS.md` | approved | DEC-CHA-01 и DEC-CHA-02 утверждены пользователем |

## Evidence на текущем шаге

- В checkout подтверждены `channels`, `channel_memberships`, encrypted credential columns, `core.ReceiveInbound`, queue/lifecycle outbound, transactional outbox и `channel.manage` permission.
- Приложенный «SMS в HTTP-шлюзе (+MAX)» визуально проверен: Omni HTTP gateway документирует POST `/messages`, `/messages/info`, inbound `/messages`, status callback `/callback`, Basic Authentication и ограничения SMS.
- Официальные материалы проверены 2026-09-11: GoTd — MTProto client и bot surface; MAX — HTTPS Bot API на `platform-api2.max.ru` и token header; Carrot quest docs доступны; MTS support подтверждает Omni endpoints/limits.

## Риски

- User-account Telegram и его session/2FA меняют security, compliance и operational scope.
- Токены, Basic credentials, OAuth secret и callback secret не должны возвращаться в UI, логироваться или попадать в outbox payload.
- Статус «отправлено» у провайдера не тождественен «доставлено»; после timeout результат может быть неопределённым и требует reconciliation, не слепого retry.
- VK и Carrot quest нужно подтвердить по точной интеграционной модели/версии и выданным scopes до реализации их конкретных request models.
