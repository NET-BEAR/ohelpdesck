# SPEC-020: outbound reply, idempotency и lifecycle

Статус: `planned`. Цель: authorised Channel member создаёт immutable queued agent reply с user-scoped idempotency, затем worker-facing command безопасно применяет `queued → sent/failed` в PostgreSQL transaction и durable outbox.

В срезе: idempotency record `(actor_user_id, key)` с request hash; queue, mark sent, mark failed; waiting/first response по DEC-020-03/04; channel membership for queue; HTTP/OpenAPI queue endpoint. Вне scope: provider sending, retries/jobs, delivered/read, attachments и UI.
