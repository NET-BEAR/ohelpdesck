# SPEC-020: outbound reply, idempotency и lifecycle

Статус: `completed`. Цель: authorised Channel member создаёт immutable queued agent reply с user-scoped idempotency, затем worker-facing command безопасно применяет `queued → sent/failed` в PostgreSQL transaction и durable outbox.

В срезе: idempotency record `(actor_user_id, key)` с request hash; queue, mark sent, mark failed; waiting/first response по DEC-020-03/04; channel membership for queue; HTTP/OpenAPI queue endpoint. Вне scope: provider sending, retries/jobs, delivered/read, attachments и UI.


## Завершение

Функциональный срез реализован и поставлен на dev: idempotent queue, lifecycle и миграция 5. CI `34576001075`, manual dev delivery `34576434582`, remote migration/race/integration/vet/gofmt подтверждены. DB adapter для infrastructure error paths вынесен в follow-up по DEC-OUT-01.
