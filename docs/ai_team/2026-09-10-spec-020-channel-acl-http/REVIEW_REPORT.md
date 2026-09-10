# Independent review — Channel ACL and HTTP boundary

- Reviewed revision: `d5c2186..23b50d6` (`23b50d69c631ec6b4ea61bd53ef211c981a78b22`)
- Date: 2026-09-11
- Verdict: **approve**

## Scope and contract

The change is confined to the planned Channel ACL/operator HTTP slice: two
operator `PATCH` commands, their OpenAPI contract, CORS preflight support,
transaction-anchored membership enforcement, runtime wiring, and integration
coverage. It does not change inbox visibility, assignment mutation, outbound
sending, provider adapters, or scheduled work.

`docs/agents/roles/reviewer.md` was not present in this checkout; the review
therefore used the task artifacts, source, tests, repository `AGENTS.md`, and
CI evidence as the applicable sources.

## Findings

No blocking or non-blocking findings.

1. **Authentication and authorization — accepted.** `conversationCommand`
   requires a valid server session and CSRF header before the effective
   `conversation.reply` permission. It parses the authenticated user ID and
   forwards it to the transition command. The response mapping distinguishes
   unauthenticated (`401`), CSRF/global permission/resource ACL (`403`),
   validation (`400`), missing Conversation (`404`), and optimistic-lock or
   transition conflict (`409`).
2. **Channel ACL and revocation race — accepted.** With an authenticated actor,
   `loadConversationForUpdate` resolves the Channel then locks that actor's
   `channel_memberships` row with `FOR UPDATE` in the same transaction as the
   Conversation lock, update, and outbox append. Absent membership or
   `can_reply=false` aborts before the Conversation write; a concurrent
   membership update/delete is serialized on the membership row. `assignee_id`
   is absent from the authorization predicate.
3. **HTTP/OpenAPI/CORS — accepted.** Both endpoint paths document cookie
   security, required CSRF header, UUID path parameter, strict request schemas,
   response Conversation schema, and all required `400/401/403/404/409`
   outcomes. CORS preflight now includes `PATCH` and `X-CSRF-Token`.
4. **Tests and scope hygiene — accepted.** Integration coverage exercises no
   session, missing CSRF, absent/revoked membership, assigned-to-another-agent
   access, invalid inputs, invalid route, missing Conversation, stale version,
   status conflict, success, unconfigured handler, and CORS preflight. The diff
   is clean (`git diff --check`); pre-existing unrelated worktree changes were
   not included.

## Evidence reviewed

- GitHub Actions run `34532417247` for `23b50d69…`: `verify` and `deploy-dev`
  completed successfully. The deployment log reports migration version `4`,
  healthy API/worker/web containers and successful readiness checks.
- Orchestrator-provided fresh remote evidence: targeted integration race suite,
  `go vet`, and `gofmt` completed successfully after deploy.
- Static review: `git diff --check d5c2186..23b50d6` produced no output.

## Residual risks

- This slice intentionally exposes mutations only. Conversation list/get/read
  endpoints and their `conversation.read`/`can_read` policy remain future
  work.
- Global permission evaluation occurs at the HTTP boundary; Channel membership
  is the authorization element locked with the write transaction, as specified
  by this slice. Future non-HTTP operator entry points must pass the actor and
  enforce the same global permission before invoking the core command.
