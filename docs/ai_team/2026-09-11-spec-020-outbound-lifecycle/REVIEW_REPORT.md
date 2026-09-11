# Independent review: outbound reply lifecycle

- **Reviewer ID:** `/root/outbound_lifecycle_review`
- **Reviewed commit:** `43264b6903522df323611aa32c443686e27813f8`
- **Scope:** outbound queue API, idempotency persistence, worker-facing `queued → sent/failed` transitions, migration 5, OpenAPI, task artifacts and integration tests.
- **Verdict:** `approve`

## Findings

No blocking or non-blocking correctness, security, transactionality, migration, or contract defects were found in the reviewed diff.

## Verified evidence

1. `internal/core/outbound.go` rejects invalid queue commands before writing; locks Channel membership through `loadConversationForUpdate`, serializes a `(actor, idempotency key)` retry with a transaction-scoped advisory lock, compares the stored canonical SHA-256 request hash, and creates the Message, key record, and `message.queued` event in one PostgreSQL transaction. A failed outbox append therefore rolls all three facts back.
2. The idempotency scope matches the required user scope: `message_idempotency_keys` has primary key `(user_id, idempotency_key)`, retains the canonical Message by FK, and rejects same-key requests with a different hash. `tests/integration/outbound_lifecycle_test.go` covers same retry, conflicting retry, and two concurrent submissions.
3. `MarkSent` and `MarkFailed` lock the Message and its Conversation in a transaction, enforce only `queued → sent`, `queued → failed`, and `sent → failed`, get mutation time from PostgreSQL `clock_timestamp()`, and append status events in the same transaction. The implementation does not make a provider call or expose provider models to core.
4. The waiting correction guard checks both that no new waiting episode exists and that the failed message is still `waiting_closed_by_message_id`. It restores the preserved original `waiting_closed_since` only in that case. The test covers covered-episode restoration and preservation of a newer inbound episode.
5. `internal/auth/outbound_http.go` requires authenticated mutation CSRF validation and `conversation.reply`; the service repeats the effective locked Channel `can_reply` check inside its transaction. API responses distinguish creation (`201`), canonical retry (`200`), validation (`400`), authorization (`401/403`), absent conversation (`404`), and idempotency conflict (`409`). `api/openapi.yaml` describes the endpoint and required headers; CORS admits `Idempotency-Key`.
6. Migration 5 is registered with the ordered migration runner and introduces only the idempotency table plus the waiting-episode timestamp required for safe correction. The down migration removes them in reverse logical order. Existing fixture cleanup clears the pre-existing `waiting_closed_by_message_id` FK before message deletion.
7. `git diff --check 70da891^..43264b6` completed without whitespace errors. GitHub Actions runs `34566347311` and the explicitly dispatched dev delivery `34567101494` both report `success` for reviewed SHA `43264b6`.

## Residual risks / follow-up

- The slice deliberately does not contain provider dispatch, retry scheduling, or delivered/read receipts. The later dispatcher slice must call lifecycle methods with bounded contexts and define handling for provider-side duplicate external IDs.
- The current HTTP response intentionally returns the canonical Message's current status on retry. Consumer/UI work should use `duplicate` to distinguish a retry acknowledgement from the original creation.
- Remote dev migration and full runtime/QA evidence remains the QA stage; this review does not substitute it.
