# Бизнес-анализ: durable dispatcher, jobs и leasing

Дата: 2026-09-11. Статус: `ready_for_review`. Основание: SPEC-030 и текущий checkout.

## Ценность и граница

После SPEC-020 система сохраняет business facts и outbound intent, но не имеет надёжного механизма продолжить асинхронную работу при остановке процесса. Эта vertical превращает committed domain event в durable work без зависимости от Redis и без claim о delivery внешнему провайдеру. Она нужна, чтобы будущая отправка `message.queued`, wakeup и другие consumers получили одинаковые гарантии восстановления.

## Участники

| Участник | Цель | Разрешённое действие |
|---|---|---|
| Core application service | Зафиксировать business mutation и event. | Только append в outbox в той же PostgreSQL transaction. |
| Dispatcher worker | Создать durable job на каждый зарегистрированный handler. | Lock undispatched event, fan-out, поставить `dispatched_at` после inserts. |
| Worker instance | Временно владеть одной job. | Claim, extend/complete/reschedule только по current lease token. |
| Registered DB-only handler | Выполнить idempotent DB effect. | Выполнить effect и receipt в одной transaction. |
| Future provider handler | Отправить реальное внешнее сообщение. | Вне scope; обязан иметь собственную provider idempotency/reconciliation. |
| Operator/admin | Диагностировать dead/pending work в будущем. | UI/manual retry вне scope. |

## As-is и To-be

**As-is:** `outbox_events` сохраняет факт доменного изменения. Если worker выключен, event durable, но его никто не fan-out-ит в job. Нет lease, retries, receipts или crash recovery.

**To-be:** Dispatcher берёт только eligible undispatched events, создаёт по одной `domain_event` job на зарегистрированного consumer и фиксирует dispatch в единой transaction. Worker берёт job по lease; после успешного DB-only effect завершает job. Temporary failures переносятся, permanent/max-attempt failures становятся `dead`; expired owner теряет право менять job и work снова доступна.

## Бизнес-правила

1. PostgreSQL — единственный источник durable event/job/receipt. Потеря Redis, restart API или worker не удаляют committed event/job.
2. Event считается dispatched только после того, как все job для его зарегистрированных handler существуют в той же committed transaction.
3. `(event_id, handler)` — duplicate guard. Повтор dispatcher, crash между inserts и commit или несколько dispatcher не создают второй durable job для одного consumer.
4. Обработка job **at least once**. Одновременно job принадлежит одному lease; у каждой claim новый opaque lease token.
5. `completed`, retry/reschedule и lease extension допустимы только для `running` job того же `locked_by` и lease token. Старый worker не может завершить или переназначить новую lease.
6. Pending job не claim-ится ранее `run_at`; cancelled/dead/completed job не claim-ятся.
7. Transient error увеличивает attempts и создаёт будущий `run_at` с bounded exponential backoff+jitter. Permanent error или exhaustion `max_attempts` переводит job в `dead`. Persisted error — только stable code и sanitized message.
8. Recovery возвращает только expired `running` job в pending, очищая ownership/token. Она не «завершает» работу за умерший worker.
9. DB-only handler сначала проверяет receipt `(event_id, handler)`. Effect и insertion receipt commit/rollback вместе. Повтор после crash после handler commit не повторяет DB effect.
10. Unknown handler — observable permanent configuration failure, не silent success. Dispatcher не исполняет names из untrusted payload.
11. Payload/errors/logs не содержат credentials, headers, full message text или provider responses. Job payload переносит stable event ID and correlation/causation IDs.
12. При отсутствии зарегистрированного route событие не считается processed автоматически. До подтверждения архитектурного routing gate оно остаётся undispatched и observable.

## Исключения

| Ситуация | Ожидаемое поведение |
|---|---|
| Dispatcher crash до commit | Ни jobs, ни `dispatched_at` не видны; retry безопасен. |
| Crash после fan-out commit до последующего poll | Jobs существуют; event dispatched; worker позже claim-ит их. |
| Two workers claim | Только один получает row/token; второй получает no job. |
| Worker dies after claim | После lease expiry recovery делает job pending; следующий worker claim-ит новую token. |
| Stale worker completes | No rows updated; state current lease remains intact; stable stale-lease outcome observable. |
| Handler DB effect commits, completion update fails | Job может повториться, но receipt делает supported DB-only effect no-op. |
| External provider outcome ambiguous | Не решается generic receipt/job; future channel adapter reconciles it. |
| Event has no route | Не set `dispatched_at`, метрика/лог показывают backlog; не создаётся fictitious job. |

## Словарь

| Термин | Определение |
|---|---|
| Domain event | Durable факт о уже committed business mutation из `outbox_events`. |
| Job | Durable отдельная единица асинхронной работы. |
| Dispatcher | Worker loop, который преобразует outbox event в jobs registered handlers. |
| Handler | Явно зарегистрированный кодовый consumer; name не приходит из внешнего payload. |
| Lease | Временное диагностическое владение job worker-ом. |
| Lease token / fencing token | Новый UUID каждой claim; predicate, который запрещает stale owner update. |
| Receipt | `(event_id, handler)` факт, записанный вместе с DB effect. |
| Retry | Future requeue transient failure; не новая business mutation. |
| Dead job | Work, остановленная permanent failure либо max attempts. |
| At least once | Handler может быть вызван повторно; duplicate effect предотвращается только handler-specific idempotency/receipt. |

## Трассировка требований

| Требование SPEC | Правило/AC этой vertical |
|---|---|
| ADR-EVT-001/004/005; DATA-EVT-003 | BR-1–3; AT-JOB-001, 002, 003 |
| ADR-EVT-002/003; DATA-EVT-002/005; FR-EVT-001/002 | BR-4–8; AT-JOB-004, 005, 006, 007 |
| §§13–16, 22–26; FR-EVT-003 | BR-7–10; AT-JOB-008–013 |
| SEC-EVT-002/003; §34–35 | BR-11; AT-JOB-014 |
| AC-EVT-004–008, 011–016, 020 | AT-JOB-001–014 |
| AC-EVT-009/010 (already core) | Regression AT-JOB-015: dispatcher does not alter atomic core/outbox semantics |

## Decision gate

**DG-030-01 — event without a registered durable handler.**

- **A (recommended):** do not mark such event dispatched; retain it until an explicit route exists and expose backlog. This preserves future fan-out and never silently loses work.
- **B:** mark event dispatched with zero jobs. Simpler now, but loses automatic processing when handler is added.
- **C:** create an unknown-handler dead job. It makes an infrastructure configuration absence look like a business failure and adds artificial dead-letter noise.

Implement only after architect records A/B/C. Recommendation A is used in provisional acceptance wording.
