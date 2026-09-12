# Architecture

Новая migration создаёт durable outbound_idempotency with request hash and Message FK. Queue transaction: lock Conversation and Actor Channel membership, lock/read idempotency key, compare canonical hash, insert queued Message + idempotency record + `message.queued` outbox event. Lifecycle transaction locks Message then Conversation, validates transition, gets `clock_timestamp()`, updates operational metadata/state and appends redacted event. No provider model is admitted into core.
