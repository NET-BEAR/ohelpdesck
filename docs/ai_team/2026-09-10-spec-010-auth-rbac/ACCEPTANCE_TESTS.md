# Acceptance tests: SPEC-010

Статус: `ready_for_review`. Эти сценарии должны перейти в RED до implementation.

1. Given active pre-provisioned local user and correct password, When login succeeds, Then `/api/v1/me` returns that user and server-computed permissions through a server-side HttpOnly session.
2. Given an invalid login/password or disabled user, When login is processed, Then it returns a neutral unauthenticated outcome and creates no session.
3. Given password, session ID or CSRF token sent to login/API, When logs and traces are captured, Then none of these secret values appear.
4. Given agent session, When it calls users, bundle or invitation mutation endpoints, Then it receives 403 and no database mutation occurs.
5. Given administrator creates or resets a user password through an explicit protected service, When persistence completes, Then only Argon2id hash persists and no API read model returns it.
6. Given any state-changing session request lacks valid CSRF input, When it reaches the API, Then it fails before mutation.
7. Given concurrent user creation with equal login or email ignoring case, When requests race, Then exactly one succeeds and uniqueness remains intact.
8. Given administrator attempts to disable or de-administer the final active administrator, When mutation is sent, Then it is rejected transactionally.
9. Given production config enables an unsupported provider or attempts legacy dev bypass, When API starts, Then startup fails without secret values in output.
10. Given repeated invalid password attempts, When the rate limit is exceeded, Then login is rejected without testing passwords further in the configured window.
11. Given migration from foundation database, When ordered migrations apply twice or status is read, Then schemas and migration records are consistent without reapplying prior SQL.
