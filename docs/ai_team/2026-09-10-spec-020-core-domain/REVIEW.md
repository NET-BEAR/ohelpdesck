# Независимый review: SPEC-020 первая core/outbox vertical

Статус: `needs_changes`. Дата: 2026-09-10.

## Область проверки

- reviewer_id: `core_domain_review` (независим от разработчика и оркестратора);
- reviewed_commit: `6a60fc3` (`14d3250` и последующие migration-test fixes);
- источники: утверждённые `ARCHITECTURE.md`, `ACCEPTANCE_TESTS.md`,
  `IMPLEMENTATION.md`, diff, production code и integration tests;
- пользовательские незакоммиченные `.gitignore`, `PLANS.md` и Chatwoot task
  artifacts не входили в review и не изменялись.

## Verdict

`needs_changes`.

Первая вертикаль действительно использует одну PostgreSQL transaction и
реальный transaction-bound outbox writer. Но обработка duplicate inbound не
имеет сериализации по настоящему idempotency key, а migration нарушает явное
ограничение утверждённой архитектуры о запрете скрытых triggers. Оба дефекта
должны быть исправлены до QA.

## Findings

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
