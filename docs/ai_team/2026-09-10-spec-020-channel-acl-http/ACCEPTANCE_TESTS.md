# Acceptance tests

- Given assigned Conversation in Channel C and Agent A with global permission plus `can_reply`, when A changes status, then 200 and mutation succeeds although assignee differs.
- Given global permission but no membership/capability, when operator changes status or priority, then 403 and version/outbox unchanged.
- Given valid membership revoked before locked write, when command commits, then 403 and no partial mutation.
- Given authenticated member, when stale `expected_version` is supplied, then 409 and current state remains.
- Given unauthenticated or missing CSRF mutation, then 401/403 respectively.
- OpenAPI documents endpoint auth, CSRF, request fields and 400/401/403/404/409 outcomes.
