# SPEC-020: Core domain и transactional outbox

Статус задачи: `in_implementation`. Дата: 2026-09-10. Владелец: оркестратор.

## Цель

Реализовать первую проверяемую core-вертикаль: уже нормализованное входящее сообщение создаёт или находит ContactIdentity и Conversation, сохраняет immutable Message, применяет правила waiting/status и в той же PostgreSQL-транзакции записывает durable outbox event. Внешние provider webhook/send, inbox UI, очереди, SLA и analytics не входят в этот срез.

## Выполнено

Бизнес-анализ прочитал SPEC-020 и необходимую зависимость SPEC-030, сверил их с текущим checkout и подготовил [RND.md](RND.md) и [BUSINESS_ANALYSIS.md](BUSINESS_ANALYSIS.md). Подтверждено отсутствие core migrations/modules/outbox; временный no-op outbox для runtime неприемлем.

## Принятые решения пользователя

Полные варианты и последствия приведены в DEC-020-01..04 [RND.md](RND.md); утверждённый контракт записан в [DECISIONS.md](DECISIONS.md):

1. Visibility/assignment: assignment не закрывает Conversation. Любой agent с доступом к Channel видит и отвечает; подпись конкретного агента включается в тело email, при общем Channel account.
2. Гонка resolve и нового inbound: рекомендуется требовать `expected_version` для operator resolve, а inbound после lock всегда открывает Conversation; альтернативы — last-command-wins или специальный приоритет inbound.
3. Authoritative clock waiting/first response: рекомендуется DB/server commit time; альтернативы — provider time или комбинированное правило.
4. Поздняя provider correction `sent → failed`: рекомендуется восстанавливать waiting только если текущий episode всё ещё покрыт этим Message; альтернативы — никогда или всегда восстанавливать.

## Следующие шаги

1. Архитектор готовит `ARCHITECTURE.md`: lock order, transaction ownership, migration/constraint design и minimum PostgreSQL outbox.
2. БА/QA формируют полный `ACCEPTANCE_TESTS.md` до кода.
3. Разработчик создаёт RED tests, затем минимальную реализацию, проходит review и QA.

## Готовность к реализации

`ARCHITECTURE.md` и `ACCEPTANCE_TESTS.md` готовы. Роль Разработчик начинает с RED integration tests с двумя PostgreSQL connections и failpoints; API/UI-сценарии, явно отложенные первым вертикальным срезом, не публикуются без отдельного contract task.
