# Независимый review: SPEC-020 operator transitions

Статус: `approve` для P1-rework. Дата: 2026-09-10.

## Область проверки

- reviewer_id: `operator_transitions_review`, независим от реализации;
- reviewed range: `57db9f5..c35ff3f`;
- проверены task artifacts, `spec/020-core-domain.md`, production diff, integration tests и предоставленное remote CI/runtime evidence;
- пользовательские незакоммиченные `.gitignore`, `docs/ai_team/PLANS.md` и `docs/ai_team/2026-09-10-chatwoot-inspired-ui/` не входили в review и не изменялись.

Файл `docs/agents/roles/reviewer.md`, указанный во входной задаче, отсутствует в checkout (поиск по репозиторию не нашёл его). Review выполнен по проектному `AGENTS.md`, task artifacts и SPEC как первичным источникам.

## Verdict

Первичный review имеет `needs_changes`. Для P1-rework на SHA
`c1e73cbb9f994718cfd4aa5de6b0a0b7b53ca9e5` verdict — `approve`.

Транзакционная реализация `FOR UPDATE → expected_version → update → outbox append` корректно защищает от stale/concurrent команд; DB time, snooze deadline, priority independence и rollback при ошибке outbox покрыты интеграционными сценариями. Первоначально published event type нарушал обязательный event contract SPEC-020; его закрытие описано в повторном review ниже.

## Findings

### P1 — status transition публикует несуществующий в SPEC event type вместо обязательных domain events

**Место:** `internal/core/conversation_transitions.go:114`; непокрытая проверка — `tests/integration/conversation_transitions_test.go:84-125, 145-160`.

Для любого status transition `ChangeStatus` создаёт `conversation.status_changed`. В `spec/020-core-domain.md:954-980` перечислены минимально обязательные события: `conversation.pending`, `conversation.snoozed`, `conversation.resolved`, `conversation.reopened` и `conversation.priority_changed`. В частности, `resolved → open` обязан давать distinct `conversation.reopened`; это также зафиксировано в `spec/020-core-domain.md:666-676` и в ранее утверждённых core acceptance tests `AT-020-030`.

Ни один из обязательных status event types не может быть получен dispatchers/consumers: `conversation.status_changed` отсутствует в нормативном перечне. Локальный `ARCHITECTURE.md:9` и acceptance `AT-TR-001..008` не могут молча сузить более высокий утверждённый SPEC contract; они сами требуют синхронизации.

**Риск:** будущий dispatcher или consumer, подписанный на нормативные event names, не обработает pending/snooze/resolve/reopen. Это создаст тихое функциональное расхождение после подключения worker/jobs.

**Требуемое исправление:**

1. Выбирать event type по transition: минимум `pending → conversation.pending`, `snoozed → conversation.snoozed`, `resolved → conversation.resolved`, `resolved → open` и `snoozed → open` — `conversation.reopened`; согласовать type для `open → pending/open` согласно SPEC/event contract, не вводя новый неописанный type.
2. Сохранить payload с stable IDs, previous/new status, version и DB occurred_at.
3. Обновить `ARCHITECTURE.md` и `ACCEPTANCE_TESTS.md` так, чтобы они перечисляли нормативные types.
4. Добавить integration assertions event_type для каждой значимой ветви, особенно `resolved → open`; повторить remote targeted/full `-race`, coverage, vet, gofmt, CI/deploy, затем независимые review и QA.

## Проверенное evidence

- `git diff --check 57db9f5..c35ff3f` и `git show --check c35ff3f`: ошибок whitespace нет.
- Матрица допустимых переходов в `internal/core/conversation_transitions.go:161-174` совпадает с `spec/020-core-domain.md:614-631`.
- `SELECT ... FOR UPDATE` и проверка version находятся в одной `WithinTx` до mutation (`internal/core/conversation_transitions.go:84-114`); integration scenario конкурирующих команд существует в `tests/integration/conversation_transitions_test.go:244-281`.
- DB server time получен через `clock_timestamp()` и используется для `resolved_at`, `updated_at`, `occurred_at` (`internal/core/conversation_transitions.go:95-114`); соответствующий interval check есть в тесте.
- Snooze требует future deadline относительно DB clock и сохраняется как timestamptz; переходы из snoozed очищают marker. SetPriority меняет только priority/version и пишет event в той же transaction.
- Failpoint `transitionFailingAppender` проверяет rollback aggregate и outbox.
- Предоставленное оркестратором evidence: CI/deploy run `34508774265` успешен; remote targeted `-race` transition tests, `go vet` и `gofmt` успешны. Эти GREEN-проверки не закрывают P1, поскольку тесты не утверждают нормативный `event_type`.

## Риски после исправления

- Нужен повторный независимый review и QA, поскольку P1 затрагивает public domain-event contract.
- WakeSnoozed, ACL/HTTP boundary и scheduled wakeup остаются явно вне этого среза; это не finding для данного diff.

---

## Повторный review P1-rework

Проверен diff `c35ff3f..c1e73cbb9f994718cfd4aa5de6b0a0b7b53ca9e5`.

### Закрытие P1: нормативные transition event types

`ChangeStatus` больше не публикует неописанный `conversation.status_changed`.
`statusEventType` выбирает нормативный type в том же transaction-bound outbox
append (`internal/core/conversation_transitions.go:114,161-179`):

- переход в `pending` — `conversation.pending`;
- переход в `snoozed` — `conversation.snoozed`;
- переход в `resolved` — `conversation.resolved`;
- `snoozed -> open` и `resolved -> open` — `conversation.reopened`;
- `pending -> open` — документированный `conversation.opened`.

Это соответствует перечню `spec/020-core-domain.md:954-980`; distinct
`conversation.reopened` для leaving resolved/snoozed соблюдён.

`TestConversationStatusTransitionMatrix` теперь задаёт ожидаемый event type
для всех девяти допустимых переходов и читает последний conversation event
из PostgreSQL (`tests/integration/conversation_transitions_test.go:84-142,
285-292`). Resolve test дополнительно подтверждает `conversation.resolved` и
version payload. Тем самым исходная непокрытая contract branch закрыта.

### Повторное evidence

- `git diff --check c35ff3f..c1e73cbb9f994718cfd4aa5de6b0a0b7b53ca9e5`
  и `git show --check c1e73cbb9f994718cfd4aa5de6b0a0b7b53ca9e5` завершились
  без whitespace errors.
- Предоставленное оркестратором remote evidence: CI/deploy run `34511482128`,
  targeted `-race` transition tests, `go vet` и `gofmt` — успешны. Оно
  соответствует добавленным assertions event type.

### Неблокирующий documentation follow-up

`docs/ai_team/2026-09-10-spec-020-operator-transitions/ARCHITECTURE.md:9`
и `ACCEPTANCE_TESTS.md:7-14` всё ещё описывают старый generic
`conversation.status_changed` либо не фиксируют нормативные types. До
закрытия task оркестратору нужно синхронизировать эти artifacts с code/SPEC
и затем приложить их к QA evidence. Это не блокирует одобрение code P1-rework,
но без него task documentation остаётся противоречивой.
