# Независимый review: SPEC-020 первая core/outbox vertical

Статус: `approve` для P1-rework. Дата: 2026-09-10.

## Область проверки

- reviewer_id: `core_domain_review` (независим от разработчика и оркестратора);
- reviewed_commit: `6a60fc3` (`14d3250` и последующие migration-test fixes);
- источники: утверждённые `ARCHITECTURE.md`, `ACCEPTANCE_TESTS.md`,
  `IMPLEMENTATION.md`, diff, production code и integration tests;
- пользовательские незакоммиченные `.gitignore`, `PLANS.md` и Chatwoot task
  artifacts не входили в review и не изменялись.

## Verdict

Первичный review на SHA `6a60fc3` был `needs_changes`; его P1 findings
закрыты повторной проверкой SHA `6e7b593b6708a2a8e65af2b6216cefa43caa48c7`.
Для ограниченного P1-rework verdict — `approve`.

Первая вертикаль действительно использует одну PostgreSQL transaction и
реальный transaction-bound outbox writer. Но обработка duplicate inbound не
имеет сериализации по настоящему idempotency key, а migration нарушает явное
ограничение утверждённой архитектуры о запрете скрытых triggers. Оба дефекта
должны быть исправлены до QA.

## Findings первичного review

### P1 — duplicate external message может создать лишний Conversation и вернуть противоречивый canonical result

**Места:** `internal/core/inbound.go:77-120`, в особенности lookup message на
строках 110-114; `tests/integration/core_domain_test.go:182-229`.

`messages_channel_external_id_uq` задаёт идемпотентный ключ
`(channel_id, external_message_id)`, но `ReceiveInbound` сначала берёт lock
только по identity/thread, создаёт Contact/ContactIdentity/Conversation, и
лишь затем проверяет Message. Повтор той же внешней доставки с другим
`external_user_id` или `external_thread_id` поэтому создаёт новый пустой
Conversation и возвращает его `ConversationID` вместе с `MessageID` исходной
доставки. При параллельной доставке с разными identity/thread оба вызова также
могут не увидеть Message и один завершится unique-violation вместо canonical
duplicate result.

Это нарушает duplicate-safe contract AT-020-024 и обещание architecture
`Find ... external Message ID` вернуть canonical result без новой mutation.
Текущий test моделирует только два полностью одинаковых input и потому
сериализуется identity/conversation locks; он не покрывает настоящий конфликт
внешнего message key.

**Исправление:** до создания любых core facts захватывать xact advisory lock,
канонически вычисленный из `(channel_id, external_message_id)`, искать
существующий Message с его Conversation/Identity/Contact и немедленно
возвращать этот canonical result. Сохранить согласованный lock order (Channel
затем message advisory, затем identity/conversation) и добавить integration
tests: (a) existing duplicate с изменёнными sender/thread не создаёт facts,
(b) concurrent same message key с разными sender/thread возвращает один
canonical result без unique-violation и без лишних facts/events/version.

### P1 — migration добавляет скрытые triggers в обход утверждённого architecture contract

**Места:** `db/migrations/000003_core_outbox.up.sql:87-114`; противоречащий
контракт — `docs/ai_team/2026-09-10-spec-020-core-domain/ARCHITECTURE.md:137-140`.

Архитектура прямо устанавливает, что channel equality должен проверять service
code и что hidden trigger не вводится без ADR. Migration добавляет две PL/pgSQL
functions и два `BEFORE INSERT OR UPDATE` triggers. Это не только отклонение
от согласованного дизайна: появившаяся database-side business logic не описана
в event/ошибочном контракте и не покрыта отдельными integration tests.

**Исправление:** убрать triggers/functions и выполнить явные cross-table
проверки в application/repository service с integration proof, как предписано
архитектурой. Если team сознательно выбирает DB-level constraint, сначала
нужны ADR и обновление architecture/acceptance с фиксированным SQLSTATE/error
mapping, concurrency rationale и test coverage; до этого вариант не
соответствует утверждённому contract.

## Проверенное evidence

1. `git show --check 14d3250` и `git diff --check 62349369..6a60fc3`:
   завершились без whitespace errors.
2. Независимо выполнено на remote dev через project tools-container (без чтения
   или вывода runtime secrets):

   ```text
   go test -count=1 -race ./tests/integration
   exit code: 0
   ok github.com/NET-BEAR/ohelpdesck/tests/integration 8.605s
   ```

3. Предоставленное runtime evidence сверено: CI verify+deploy run `34492044996`,
   migration version 3; remote race integration, coverage 83.9%, vet и gofmt
   заявлены GREEN. Эти результаты не закрывают два описанных negative/concurrent
   duplicate сценария, так как соответствующих test cases нет.

## Риски и повторная проверка

- До P1-исправления provider retry/collision может оставить пустые
  Conversation/Contact facts или вернуть caller несовместимые IDs; это создаёт
  неверную рабочую очередь и события.
- До устранения architectural drift schema содержит недокументированную
  business logic, которую будет трудно безопасно изменять при следующих
  vertical slices.
- После изменений повторить remote integration/race/coverage/vet/gofmt, затем
  независимый review и QA. В review обязательно предъявить оба новых duplicate
  сценария и проверку отсутствия лишних Contacts/Identities/Conversations,
  Messages, version increments и outbox events.

---

## Повторный review P1-rework

Проверен diff `6a60fc3..6e7b593b6708a2a8e65af2b6216cefa43caa48c7`.

### Закрытие P1: canonical duplicate inbound

`ReceiveInbound` теперь после lock/read Channel берёт xact advisory lock по
каноническому `(channel_id, external_message_id)` и вызывает
`findCanonicalInbound` **до** identity/conversation resolution
(`internal/core/inbound.go:77-93`). Canonical lookup возвращает Contact,
Identity, Conversation и Message из одной уже сохранённой path, проверяя
channel/contact consistency (`internal/core/inbound.go:158-180`). Поэтому
изменённые provider sender/thread при retry больше не создают пустые facts.

Новые remote integration tests проверяют именно пропущенные ранее сценарии:

- последовательный duplicate с другими sender/thread возвращает все исходные
  IDs и сохраняет ровно один Contact/Identity/Conversation/Message, два
  исходных events и version `1`
  (`tests/integration/core_domain_test.go:116-163`);
- два конкурентных duplicate с разными sender/thread дают один canonical
  result без unique-violation и без дополнительных facts/events/version
  (`tests/integration/core_domain_test.go:195-253`).

### Закрытие P1: скрытые triggers

Applied migration `000003` не переписана. Новая checksummed migration
`000004_remove_core_channel_triggers` удаляет оба triggers и обе PL/pgSQL
functions, а version 4 зарегистрирована в migrator
(`db/migrations/000004_remove_core_channel_triggers.up.sql:1-4`,
`internal/platform/database/migrations.go:17`). Явные service checks для
identity, conversation и canonical path добавлены в
`internal/core/inbound.go:99-133,158-180`. Это соответствует первоначальному
architecture contract, не меняя уже применённую migration 3.

### Повторное evidence

Независимо на remote dev выполнено только через project tools-container,
без чтения или вывода runtime env/secrets:

```text
go test -count=1 -race ./tests/integration -run
  TestReceiveInbound(Duplicate|RejectsInconsistentCanonicalMessagePath)
exit code: 0
ok github.com/NET-BEAR/ohelpdesck/tests/integration 1.186s
```

`git show --check 6e7b593b` не сообщил ошибок. Новый migration имеет
согласованный `up`/`down`: forward удаляет legacy triggers/functions, rollback
восстанавливает их ровно как прежнюю версию 3. Это не меняет production code
и не скрывает rollback-semantics.

### Неблокирующее follow-up

`ARCHITECTURE.md:151-172` всё ещё описывает прежний порядок
`Channel → identity → conversation → message`. Перед следующим core mutation
его нужно синхронизировать с реализованным порядком
`Channel → message-idempotency → identity → conversation`, чтобы последующие
writer-ы не вводили конфликтующий lock order. Это не блокирует P1-rework:
нынешний code и implementation evidence описывают фактическую гарантию, а
других core mutators пока нет.
