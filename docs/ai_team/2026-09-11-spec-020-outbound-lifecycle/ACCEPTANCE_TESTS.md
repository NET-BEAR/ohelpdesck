# Acceptance tests

- Same actor/key/request returns one queued Message/event; changed request with same key yields conflict.
- Concurrent same-key requests yield one Message/idempotency record.
- Queue requires reply membership and CSRF; assignee does not restrict member.
- queued→sent closes waiting and sets first_response once using DB time; later sent reply preserves first_response.
- queued→failed keeps waiting; sent→failed restores only the message-covered episode and emits corrective event; newer episode remains intact.
- Invalid status transition, stale/not found or outbox failure leaves Message/Conversation/idempotency atomically unchanged.
- HTTP/OpenAPI documents queue success plus 400/401/403/404/409.
