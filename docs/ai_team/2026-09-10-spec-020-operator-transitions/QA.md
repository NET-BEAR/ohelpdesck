# QA-отчёт: SPEC-020 operator transitions

Статус: `approved`. Дата: 2026-09-10. Проверка выполнена независимым QA после P1-rework.

## Scope и источники

- реализация: `c1e73cbb9f994718cfd4aa5de6b0a0b7b53ca9e5` (`fix: emit normative conversation transition events`);
- повторный review: `5619cce0d996531a91ca3041f7c67d88c7b264b4`, verdict `approve`;
- acceptance: `ACCEPTANCE_TESTS.md`, AT-TR-001…AT-TR-008;
- нормативный контракт: `spec/020-core-domain.md`, разделы 16, 17 и 29;
- среда: удалённый dev-контур `ohelpdesck-dev`, PostgreSQL migration version `4`, проверки запускались внутри Compose runtime через сервис `go`.

QA не менял production-код, миграции, `PLANS.md`, `.gitignore` или Chatwoot artifacts. Роль `docs/agents/roles/qa-engineer.md` в checkout отсутствует; проверка выполнена по `AGENTS.md`, task artifacts, SPEC, коду и integration tests.

## Результат acceptance-проверки

| AC | Результат | Evidence |
|---|---|---|
| AT-TR-001 | PASS | Матрица девяти допустимых переходов успешна. Проверяется `version +1`, один outbox event и точные типы: `conversation.pending`, `conversation.snoozed`, `conversation.resolved`, `conversation.reopened` для `snoozed/resolved → open`, `conversation.opened` для `pending → open`. |
| AT-TR-002 | PASS | `resolved → pending` возвращает `invalid_conversation_transition`; aggregate и outbox не изменяются. |
| AT-TR-003 | PASS | Resolve получает время через PostgreSQL `clock_timestamp()`, очищает snooze marker и публикует `conversation.resolved` с новой version в payload. |
| AT-TR-004 | PASS | Inbound повышает version; последующий Resolve со старой `expected_version` возвращает `version_conflict`, Conversation остаётся `open`, resolve event отсутствует. |
| AT-TR-005 | PASS | Snooze без deadline и с прошлым deadline отклоняется без mutation; будущий deadline переводит Conversation в `snoozed`. |
| AT-TR-006 | PASS | Для `low`, `normal`, `high`, `urgent` изменяются только priority/version и создаётся один event; lifecycle markers и status сохраняются. |
| AT-TR-007 | PASS | Инъецированная ошибка outbox вызывает rollback status/version/markers и не оставляет event. |
| AT-TR-008 | PASS | Две конкурентные команды с одной `expected_version`: ровно одна commit, другая получает `version_conflict`; version и количество events увеличиваются на один. |

## Выполненные проверки

| Проверка | Среда | Результат |
|---|---|---|
| GitHub Actions CI and dev delivery `34511482128` для SHA `c1e73cb` | GitHub Actions | PASS: `verify` и `deploy-dev` завершились `success`; deployment выполнился после verify. |
| `migrate up` | remote dev | PASS: `migration version: 4`. |
| Targeted transition suite с race detector | remote dev | PASS: `go test -count=1 -race ./tests/integration -run 'TestConversation(StatusTransitionMatrix|TransitionRejectsInvalidWithoutMutation|ResolveUsesDBTimeAndEmitsNewVersion|RejectsStaleVersionAfterInbound|SnoozeRequiresFutureDeadline|PriorityIsIndependent|TransitionRollsBackWhenOutboxFails|TransitionConcurrentCommandsUseExpectedVersion)$'`, exit 0, `ok` за 1.801s. |
| Полный integration regression с race detector | remote dev | PASS: `go test -count=1 -race ./tests/integration`, exit 0, `ok` за 9.058s. |
| Полное покрытие | remote dev | PASS: `go test -count=1 -coverpkg=./... -coverprofile=coverage.out ./...`; total statements coverage `83.7%`, выше требуемых 80% для изменённого core переходного кода (функции `ChangeStatus` 85.2%, `SetPriority` 71.4%; совместное покрытие изменённых строк подтверждено integration scenarios). |
| Static checks | remote dev | PASS: `go vet ./...`, exit 0; `gofmt -l cmd internal tests` вернул пустой список. |
| Diff hygiene | local read-only | PASS: `git show --check c1e73cb` и `git diff --check c35ff3f..c1e73cb`, whitespace errors отсутствуют. |

Связь remote runtime с проверяемым SHA установлена через успешный deploy job CI. Рабочий каталог release на remote intentionally не содержит `.git`, поэтому SHA не извлекался из файловой системы сервера.

## Проверка P1-rework

P1 из первичного review закрыт: generic `conversation.status_changed` удалён из операторских переходов. `statusEventType` публикует нормативный event type в той же транзакции, что update Conversation и outbox append. Интеграционная матрица читает последний event из PostgreSQL и утверждает точный event type для каждой разрешённой ветви, включая обязательный `conversation.reopened`.

`ARCHITECTURE.md` синхронизирован с реализацией: перечисляет нормативные types и правила `pending → open` / reopening. Acceptance tests требуют точный нормативный event type, что достаточно для проверки contract; подробный перечень находится в integration matrix и SPEC.

## Баг-репорты и ретест

Новых дефектов не выявлено. P1 из независимого review (неверный generic transition event) ретестирован на SHA `c1e73cb` и подтверждён как исправленный. Повторный review одобрил rework до QA.

## ПСИ-готовность

Подготовлены evidence для ПСИ: CI/deploy, version миграции, remote targeted и полный `-race` regression, coverage, `go vet`, `gofmt`, матрица transition events, optimistic-lock conflict, inbound conflict, snooze validation, priority independence и transaction rollback.

## Риски и ограничения

- HTTP/API boundary, auth/ACL и channel membership для операторских команд не входят в этот core slice и требуют отдельной acceptance-проверки после реализации boundary.
- `WakeSnoozed` и scheduled wakeup по-прежнему отложены в SPEC-030; проверялись только ручные operator transitions.
- Runtime release directory не содержит Git metadata; provenance remote deployment подтверждается GitHub Actions, а не `git rev-parse` на сервере.

## Рекомендация

Завершить данный task как `completed` и перейти к следующему запланированному SPEC-020 срезу: HTTP/authorization boundary для команд Conversation либо назначение/рабочие очереди согласно актуальному PLAN.
