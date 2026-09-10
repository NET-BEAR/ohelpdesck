# Архитектура SPEC-010

Статус: `ready_for_review`.

## Границы

```mermaid
sequenceDiagram
  participant U as User browser
  participant A as support-api
  participant P as PostgreSQL
  U->>A: POST /api/v1/auth/login (login/password)
  A->>P: Load active user; verify Argon2id hash
  A->>P: Create server-side session
  A-->>U: HttpOnly session cookie
  U->>A: /api/v1/me + CSRF on mutation
  A->>P: Load active user and effective permissions
```

The API owns password verification, session store and CSRF validation. Browser does not persist credentials after login. Authenticator returns a request-scoped principal. Authorizer is a pure policy service with DB-backed effective grants and never trusts role or user ID from a request body/header.

Passwords are compared with Argon2id hashes using fixed configuration bounds and no log output. Login failures have a neutral response, bounded server-side rate limiting and do not distinguish missing user, disabled user or wrong password. The future OIDC adapter performs discovery/JWKS validation but is not enabled or reachable in this slice.

## Security invariants

- Session cookie: `HttpOnly`, `Secure` when `ENVIRONMENT=production`, scoped path, explicit SameSite policy; state-changing endpoints require CSRF validation.
- Password: never stored or logged in raw form; only an Argon2id encoded hash is persisted. An initial password source is consumed only by an explicit bootstrap command and never exposed by API.
- Login attempts are rate-limited by a non-sensitive key and session identifiers are opaque, randomly generated, hashed at rest and rotated on login.
- Every `/api/v1` endpoint is authenticated except explicitly listed infrastructure/login callback routes. `401` and `403` remain distinct.
- Local database role/bundle grants are authoritative. No arbitrary permission catalog entry, self-escalation or last-admin removal.

## Target configuration

Required configuration for this slice: `AUTH_PROVIDER=local_password`, cookie policy and rate-limit settings. The cookie is an opaque random ID; its SHA-256 hash, expiry and CSRF hash live server-side, so no session signing secret is needed. Bootstrap runs once through `bootstrap-admin` with runtime-only `INITIAL_ADMIN_PASSWORD`; it is not an API endpoint. `INITIAL_ADMIN_EMAIL` defaults to `sysadmin@local.invalid` and may be set before the first bootstrap. Secret material comes from the existing host runtime secret file/CI secret storage and must not be committed or supplied in chat. Future Keycloak configuration (`OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_AUDIENCE`, `OIDC_REDIRECT_URL`) is absent until that adapter is selected.
