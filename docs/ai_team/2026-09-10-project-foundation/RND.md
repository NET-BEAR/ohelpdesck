# Анализ исходных спецификаций

Статус: ready_for_review  
Дата: 2026-09-10  
Владелец: Архитектор  
Scope: все пять `spec/*.md`; анализ, без изменения production-кода и исходных спецификаций.

## Источники и границы доказательств

Прочитаны `spec/README.md`, SPEC-000, SPEC-010, SPEC-020, SPEC-030 полностью. На момент первичного обследования вне `spec/` обнаружен `LICENSE`; реализации Go/React, тестов и утверждённых task artifacts не было. `docs/agents/roles/architect.md` отсутствует. Прочитан portable skill AI Team. Метки `ready` подтверждены в файлах; они не доказывают доступность зависимостей или успешные тесты. Работы других участников, появившиеся после обследования, подлежат отдельному review.

Memory MCP, Jira, Confluence, `.env` и удалённая БД не использовались. API/провайдерные гарантии по этим документам не считаются проверенными внешними контрактами. Анализ GPT-6 Astra и обследование dev-host принадлежат оркестратору и не подменяются предположениями здесь.

## Что представляет собой проект

Подтверждено: собственная support-платформа, модульный монолит Go + React, использующий Chatwoot как продуктовый ориентир. Код и API Chatwoot не требуется переносить. PostgreSQL хранит durable состояние; Redis обслуживает вспомогательные функции; S3 хранит blobs. `support-api` и `support-worker` разделены для lifecycle/scaling, но используют общие модули и БД. Обязательны explicit SQL/transactions, OpenAPI 3.1 и изоляция провайдеров.

Целевая цепочка: доверенный вход провайдера → durable inbound event/job → нормализация → контакт/identity/conversation/message/outbox в одной транзакции → durable fan-out → идемпотентные обработчики. Оператор работает через OIDC, локальный user и серверный RBAC. Отправка считается успешной после принятия провайдером; queued/failed/AI сообщения не закрывают ожидание клиента.

## Готовность и зависимости

| Спецификация | Объявлено | Фактический вход для реализации | Выход и gate |
|---|---|---|---|
| 000 Foundation | ready, P0, без зависимостей | Можно начинать; выбрать безопасные инфраструктурные детали | 17 задач, 12 AC; ни одной domain-таблицы, кроме migration bookkeeping |
| 010 Auth | ready, P0, зависит 000 | Foundation + решение session/bearer, IdP/первый администратор | 14 задач, 14 AC; OIDC contract и RBAC/security tests |
| 020 Core | ready, P0, зависит 000/010 | Auth + уточнение конкурентных бизнес-правил + durable outbox интеграция | 20 задач, 22 AC; атомарность, duplicate races, waiting episodes |
| 030 Events | ready, P0, зависит 000/020 | Схема core для inbound FK; согласованный Event/OutboxWriter | 19 задач, 20 AC; F1–F7/F9, leases, receipts, fan-out |
| 040–190 | Только каталог | Нет текстов, API, критериев и readiness | Требуется refinement до реализации |

Всего 70 обозначенных задач и 68 AC в четырёх спецификациях. Это объём декомпозиции, не оценка трудозатрат. «MVP» в тексте не означает, что первые четыре спецификации дают готовый inbox: нет вложений, работающих каналов и операторского inbox UI.

## Противоречия и пробелы

Все `GAP-*` ниже — идентификаторы анализа, а не новые утверждённые требования. Где нормативный абзац не имеет requirement ID, приведены разделы и ближайшие AC: исходники требуют ID у каждого нормативного требования, но часть правил, SQL и интерфейсов остаётся без них.

| ID / этап | Связь с требованиями | Наблюдение и риск | Предлагаемое действие |
|---|---|---|---|
| GAP-01 / 020–030 | AR-004; FR-CORE-003/004; DATA-EVT-003; AC-EVT-009/010 | 020 требует durable outbox, но его реализация вынесена в зависящий от 020 SPEC-030 | Делать event contracts в 020, затем минимальный durable outbox slice 030; общий интеграционный gate. No-op writer допустим только unit fake, запрещён в runtime |
| GAP-02 / 010 | §18; SEC-AUTH-004/010; AC-AUTH-001/013 | Не выбраны cookies/bearer, login flow и способ bootstrap первого локального администратора при запрете unknown subjects | ADR до auth UI/runtime: рекомендуются same-origin server session + OIDC code/PKCE; альтернативой bearer в памяти браузера. Требуются IdP issuer/audience и controlled provisioning |
| GAP-03 / 010–020 | permission matrix; FR-AUTH-003; AC-AUTH-008; DATA-CORE-001 | «own/allowed» не определено до queues SPEC-100; нет точных permissions для status/priority/self-assign/audit | Утвердить временный resource policy; не считать наличие conversation.read правом на любую запись |
| GAP-04 / 020 | §15/31; FR-CORE-001; AC-CORE-004/006 | FOR UPDATE не блокирует отсутствующую current conversation; duplicate first messages могут создать две conversation | Сериализовать lookup/create по существующей identity row; тест двух первых разных сообщений для одного thread |
| GAP-05 / 020 | DATA-CORE-010 | Правило «второй переоценивает после lock» не гарантирует «inbound ultimately open», если resolve получает lock после inbound | Продуктовое решение: last serialized command wins либо resolve с ожидаемой version и конфликтом. Рекомендуется optimistic version для operator resolve; нужен утверждённый HTTP/command contract |
| GAP-06 / 020 | DATA-CORE-007/008/009; AC-CORE-010–017 | T не определён как received time или provider time; поздний sent callback может очистить новое waiting episode | Зафиксировать clock и границу reply coverage; тест out-of-order inbound и повторного callback после нового inbound. Не менять алгоритм молча |
| GAP-07 / 020 | §23; FR-CORE-005; AC-CORE-013/014 | sent→failed разрешён, но восстановление waiting/first_response после definitive failure не определено | Утвердить бизнес-семантику корректировки и отдельный event; fixture sent→new inbound→failed |
| GAP-08 / 020 | DATA-CORE-001/002; §9.6–9.8 | FK не доказывают contact_id=identity.contact_id; reply_to и last_read_message могут ссылаться на чужую conversation | Application validation + PostgreSQL integration tests; рассмотреть composite FK без trigger ADR; immutable identity/channel связи |
| GAP-09 / 020 | DATA-CORE-005; AC-CORE-005 | external message ID unique per channel предполагает провайдерный scope, который в channel specs ещё не доказан | Адаптер обязан нормализовать message key с thread/account context по контракту провайдера; проверить до Telegram/VK/MAX/Email |
| GAP-10 / 020 | FR-CORE-007/008; AC-CORE-018/019 | Не определены canonical request hash, пустой key, максимальная длина, TTL и область payload hash | Канонический hash включает conversation/reply/content, scope user; не истекать до согласованного окна retries; конфликт при различии |
| GAP-11 / 020 | §33; AC-CORE-020 | (created_at,id) даёт порядок, но timestamps создаются до commit; поздний commit может попасть до read cursor | Определить видимость и допустимость этого edge case; тест late commit. Если нужна строгая unread семантика — последовательный message ordinal на conversation |
| GAP-12 / 030 | FR-EVT-001/002; AC-EVT-008; claim/recovery SQL | Recovery всегда pending, claim не проверяет attempts < max_attempts; падения до handler failure могут бесконечно превышать budget | Recovery переводит исчерпанные attempts в dead; CHECK неотрицательных attempts/положительного max; crash-loop test |
| GAP-13 / 030 | DATA-EVT-004/005; AC-EVT-012/013 | lease fencing защищает job row; check receipt → effects без lock сам по себе допускает конкуренцию | Сериализовать обработку (event,handler), либо вставлять receipt с conflict guard в той же транзакции до effects; уникальная коллизия обязана откатить все effects |
| GAP-14 / 030 | §19; FR-EVT-004/005; AC-EVT-001 | processing→core→processed не имеет явно общей transaction ownership; зависший processing после crash | Нормализация вне DB tx, core effects + processed атомарно через один owner либо отдельный идемпотентный recovery contract; не создавать вложенные бизнес-транзакции |
| GAP-15 / 030 | §9/21; AC-EVT-011/016 | Dispatch с нулём handlers и rolling deployment registry changes могут навсегда пропустить будущий consumer | Versioned handler registry/manifest и явный replay/backfill; неизвестный handler observable permanent error; не обещать исторический replay автоматически |
| GAP-16 / 030 | jobs_type_dedup_active_uq; SEC-EVT-004 | Active dedup не защищает completed/dead; receipt cascade при очистке outbox уничтожает защиту replay; cleanup retention пока не реализован | Согласованная retention для inbound/jobs/events/receipts + tombstones где нужны; replay проверять после cleanup |
| GAP-17 / 020–040 | SEC-CORE-001/002; §9.2/11/12 | Encryption key lifecycle не определён; типы attachment refs не определены до 040; HTML хранится untrusted | Не принимать реальные credentials до key-management ADR; минимальные opaque ref contracts без загрузки; foundation UI не рендерит HTML сообщений |
| GAP-18 / 000 | FR-FND-008/009; §12 | Фраза Redis «optional ... only if features require it» двусмысленна | Required dependency при включённой функции; в foundation Redis/S3 checks degraded, PostgreSQL required. Явная feature/dependency policy |
| GAP-19 / delivery | NFR-FND-001; AC-FND-001/008/009; README DoD | Не указан hosting runner/registry, deploy trigger, rollback target/backup, отдельный dev secret scope | Изолированный app stack и protected dev environment; immutable commit artifact; read-only host inventory до изменения existing workloads |
| GAP-20 / governance | spec README §5/8 | Ready status объявлен раньше доступности зависимостей; существенная часть норм без стабильных IDs | Считать ready разрешением refinement; plan gate хранит реальные dependencies/evidence. При уточнениях добавлять IDs без переиспользования |

GAP-02/03/05/06/07 требуют согласованных бизнес/security контрактов до соответствующей реализации. GAP-01 и остальные транзакционные пробелы требуют review уточнения. Они не блокируют SPEC-000. Не вводить multi-tenancy, Kafka, microservices, customer login или AI в foundation.

## Безопасные уточнения foundation

1. Один canonical Compose flow: Postgres/Redis/MinIO и приложение; зависимости не требуют host-инсталляции. Dev credentials генерируются/передаются отдельно, не production defaults.
2. `/health/live` возвращает 200 без сетевых вызовов; `/health/ready` проверяет Postgres с коротким timeout, отдаёт 503 при обязательном сбое. Redis/S3 имеют отдельные bounded probes/status.
3. Config immutable, environment policy централизована; validate required values без печати значений. Разделить malformed config (fail startup) и временно недоступную optional dependency (degraded).
4. `TxManager.WithinTx` commit только при успехе; rollback на error/panic, propagation ошибок commit, rollback не скрывает исходную ошибку. Репозитории получают executor.
5. Миграции — explicit command/deploy stage с сериализацией. Foundation не создаёт users/jobs/outbox; проверка AC-FND-012 через каталог таблиц.
6. `X-Request-ID` ограничить длиной/допустимыми символами; генерировать на некорректном входе. Ошибки mapping без SQL/stack/connection URL. Body cap и timeouts до бизнес handlers.
7. Metrics на внутреннем listener, наружу только разрешённые health/API/UI. Labels по bounded route patterns, без IDs и raw URL. Redaction sentinel tests охватывают logs/traces.
8. UI содержит shell, health, error boundary, API/query client и routing. Это техническая foundation-страница, не проектирование inbox вместо отсутствующего UX.
9. CI разделяет unit/race/static, frontend locked install/typecheck/lint/test/build, real Postgres/Redis/MinIO integration, OpenAPI negative-check и generated drift при появлении генерации.

## Рекомендуемый staged roadmap

| Этап | Содержание | Условие перехода |
|---|---|---|
| A | SPEC-000 000-01..17 + dev delivery | Все 12 AC с fresh command/exit evidence; независимые review и QA; remote smoke отдельно от local |
| B | Уточнить OIDC transport/bootstrap/visibility, затем SPEC-010 | 14 AC, local JWKS rotation/claims, fail-closed production config, RBAC enforcement |
| C | SPEC-020 domain/contracts/schema + минимальный outbox SPEC-030 | Решены GAP-01/03..11, atomic real Postgres tests; нельзя выпускать runtime с fake outbox |
| D | Полный SPEC-030 worker/dispatch/receipts | 20 AC, F1–F7/F9, deterministic concurrency, F8 признан ambiguous |
| E | Написать/утвердить 040 и один pilot channel, затем 090 inbox | Ingress signature/dedup, provider key scope, delivery reconciliation, attachment security, UX/acceptance |
| F | Остальные channels и 100–150 по продуктовым приоритетам | Каждая spec проходит отдельный ready gate; бизнес-метрики и rollout |
| G | 160 AI copilot, 170–190 hardening/resilience/readiness | Реальный model/API contract и evals, optional AI failure isolation; нагрузки и production acceptance |

Security baseline не откладывается до 170: секреты, RBAC, HTML isolation и durable failure behavior являются обязательными ранее. Нагрузочные ориентиры 030 (50 inbound/s burst, 20 outbound/s, 100 agents) — target, не измеренная производительность.

## Проверки этого анализа

Выполнены чтение всех пяти файлов, инвентаризация репозитория, поиск requirement IDs. Никакие runtime/CI/provider проверки этим анализом не выполнены. Код не изменён. Обнаружены потенциальные дефекты требований, а не воспроизведённые runtime bugs. Для ПСИ foundation требуется отдельная матрица AC-FND-001..012 и свежие результаты реализации.
