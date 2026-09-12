# Решения SPEC-020 Core Domain

Статус: `approved`. Дата: 2026-09-10. Источник: явные ответы пользователя.

## DEC-020-01: командная видимость и подхват диалогов

Утверждён вариант B с уточнением: диалоги не являются приватными по assignment. Любой agent, имеющий разрешение на Channel, может видеть историю и отвечать, чтобы подхватить работу коллеги. `assignee_id` остаётся операционным полем и сигналом ответственности, но не ACL. Персональная подпись агента добавляется в тело письма; отправка использует единый email/account Channel.

Следствие: architecture должна ввести Channel-level access policy и не добавлять фильтр `assignee_id=current_user` в read/reply authorization. Assignment может использоваться routing/metrics/UI, но не закрывает Conversation.

## DEC-020-02: resolve и новое inbound сообщение

Утверждён вариант B: operator resolve передаёт `expected_version`. При конфликте version команда возвращает conflict и UI перечитывает Conversation. Inbound после получения требуемой блокировки всегда открывает Conversation.

## DEC-020-03: authoritative time

Утверждён вариант B: `waiting_since`, `first_response_at` и время ответа фиксируются по московскому времени в момент server/PostgreSQL commit. В базе значения хранятся как timezone-aware timestamps; API/UI отображают их в `Europe/Moscow`. Provider timestamp, когда появится, является описательным отдельным полем.

## DEC-020-04: поздняя коррекция delivery

Утверждён вариант B: переход `sent → failed` восстанавливает waiting только если Message всё ещё покрывает текущий waiting episode; создаётся corrective domain event. Изменение не перезаписывает более новый episode.
