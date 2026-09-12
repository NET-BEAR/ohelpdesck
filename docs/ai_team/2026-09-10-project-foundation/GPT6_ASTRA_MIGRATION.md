# План перехода проекта на GPT-6 Astra

Статус: план готов; интеграция и доступ к модели не проверены. Дата проверки официальной документации: 2026-09-10.

## Исходное состояние и смысл миграции

В исходном checkout нет приложения, OpenAI SDK, model registry, промптов, evals или вызовов API. Поэтому существующий provider adapter мигрировать пока не из чего. SPEC-000 исключает AI; SPEC-160 AI copilot только указан в каталоге. Запрошенный результат — план для точной модели **GPT-6 Astra**, а не замена моделей текущего Codex-сеанса или добавление AI в foundation.

## Подтверждённый целевой контракт

Официальный источник: [OpenAI, Model guidance — GPT-6 Astra](https://developers.openai.com/api/docs/guides/latest-model), открытый после поиска точной модели. Выжимка ниже относится к версии документации на указанную дату; перед реализацией повторить проверку.

- Model ID: `gpt-6-astra`.
- Для tool calling нужен Responses API; модель также поддерживает Chat Completions, но tools требуют смены endpoint.
- Если прежний reasoning был `none`/`minimal`, стартовать с `low`; иначе сохранить эффективный effort и сравнить результат.
- Удалить `temperature`, `top_p`, `top_logprobs`; дополнительно несовместимые logprobs поля соответствующего endpoint.
- При переходе с GPT-5.5 и ранее заменить `prompt_cache_retention` на `prompt_cache_options.ttl="30m"`.
- EU data residency требует Standard processing: fast/priority для Astra там не поддерживаются.

Это сведения документации, а не результат запросов из аккаунта проекта. Доступность, квоты, стоимость одного support-сценария и latency ещё не измерены. API-ключи для составления плана не нужны.

## Предлагаемая архитектура SPEC-160

AI copilot — необязательный модуль модульного монолита. Application service собирает разрешённый контекст, отдельный adapter вызывает Responses, возвращает типизированный результат. Сбой AI не блокирует получение, сохранение и отправку сообщений (AR-006). Использовать общий bounded HTTP client, отмену через context, redaction и ограниченную concurrency; не создавать отдельный микросервис без ADR.

Начальные use cases для согласования: summary диалога и черновик ответа оператору. Автоматическая отправка клиенту не считается разрешённой этим планом. Включение tools возможно только после перечня дозволенных действий и review проверки прав на сервере. Содержимое сообщений клиента — недоверенные данные, не системные инструкции.

## Задачи и зависимости

| Шаг | Что сделать | Вход / критерий завершения |
|---|---|---|
| A1 | Создать SPEC-160: use cases, роли, права, передаваемые поля, хранение/retention, UX | Core/Auth/Events готовы; отдельное решение по внешней передаче контекста |
| A2 | Проверить проектный доступ к `gpt-6-astra`, лимиты, регион, бюджет | Секрет передан штатным способом; успешный минимальный smoke без реальных клиентских данных |
| A3 | Определить интерфейс AI provider и JSON output contracts | Summary/draft/refusal/incomplete/timeout явно различимы, схема валидируется |
| A4 | Реализовать Responses adapter | Endpoint/параметры совместимы; mocks проверяют outbound request и parsing всех terminal states |
| A5 | Подготовить небольшой версионированный prompt и synthetic eval corpus | Не переносить будущие нерелевантные инструкции; provenance и expected outcomes у каждого примера |
| A6 | Baseline и сравнение | Качество, фактическая стоимость, p50/p95, timeout/refusal/format error rate измерены на одинаковых кейсах |
| A7 | Shadow/dev режим | Оператор сравнивает результат; отправка клиенту не вызывается; отказ AI не меняет core latency/SLO |
| A8 | Ограниченное включение и откат | Feature flag выключает AI; fallback сохраняется только если явно выбран и протестирован; review + QA |

Кандидатные пути будущей реализации: `internal/ai/` (application/contracts), `internal/ai/openai/` (adapter), `internal/platform/config/` (secret reference/model/timeouts), `tests/fixtures/ai/`, `api/openapi.yaml`, `web/src/`. Это целевые пути, не подтверждённые существующие компоненты. Embeddings, если понадобятся, выбираются отдельно; Astra не подставляется вместо embedding model.

## Acceptance/evals перед включением

1. Given summary/draft request, When Responses завершён, Then схема и русский текст валидны, нет вымышленных фактов о клиенте.
2. Given чужая conversation, When AI endpoint вызван, Then RBAC отвергает запрос до чтения контекста/внешнего вызова.
3. Given prompt injection в сообщении, When copilot строит ответ, Then не выполняет чужие инструкции и не раскрывает secret/system content.
4. Given 429/5xx/timeout/refusal/incomplete, When adapter завершается, Then typed outcome, ограниченный retry только там, где безопасно, UI позволяет продолжить ручную работу.
5. Given отсутствие OpenAI/ключа, When входящие/исходящие сообщения обрабатываются, Then durable messaging проходит без AI.
6. Given отключение feature flag, When следующий запрос приходит, Then AI больше не вызывается, существующие сообщения не изменяются.
7. Given eval corpus, When target сравнивается с baseline, Then измерения качества, цены и latency опубликованы; численные launch thresholds согласованы в SPEC-160, а не придуманы в foundation.

Async tools, mid-turn steering и dynamic reasoning не требуются первым summary/draft сценариям. Их внедрение — отдельная доказанная потребность, а не обязательная часть перехода на модель.
