# SPEC-010 Authentication, Users and RBAC

Status: ready  
Priority: P0  
Owner: identity/platform  
Depends on: SPEC-000

## 1. Goal

Implement secure authentication, local user records and explicit role-based authorization for internal support operators.

The production design is OIDC-first and compatible with a corporate identity provider. Development/test environments may use a local identity provider.

## 2. Non-goals

Not included:

- customer authentication;
- public signup;
- password reset for production users;
- social login;
- SCIM provisioning;
- fine-grained custom roles;
- queue membership/routing logic (SPEC-100);
- SaaS multi-tenancy.

## 3. Terms

### Identity Provider

External system that authenticates a human and returns a verified subject.

### User

Local application record representing an operator, supervisor or administrator.

### Subject

Stable external identifier from the identity provider:

```text
issuer + subject
```

Email is not a stable authentication key.

## 4. Roles

Exactly three built-in roles in MVP:

```text
agent
supervisor
administrator
```

Role hierarchy must not be implemented as a numeric "higher role automatically inherits everything" shortcut. Permissions are explicit.

## 5. User statuses

```text
active
disabled
```

A disabled user cannot create a new authenticated application session and cannot call authenticated API endpoints.

## 6. Database schema

Migration creates:

```sql
CREATE TYPE user_role AS ENUM (
  'agent',
  'supervisor',
  'administrator'
);

CREATE TYPE user_status AS ENUM (
  'active',
  'disabled'
);

CREATE TABLE users (
  id              uuid PRIMARY KEY,
  email           text NOT NULL,
  name            text NOT NULL,
  role            user_role NOT NULL,
  status          user_status NOT NULL DEFAULT 'active',

  external_issuer text,
  external_sub    text,

  last_login_at   timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT users_oidc_identity_complete CHECK (
    (external_issuer IS NULL AND external_sub IS NULL)
    OR
    (external_issuer IS NOT NULL AND external_sub IS NOT NULL)
  )
);

CREATE UNIQUE INDEX users_email_lower_uq
  ON users (lower(email));

CREATE UNIQUE INDEX users_external_identity_uq
  ON users (external_issuer, external_sub)
  WHERE external_issuer IS NOT NULL AND external_sub IS NOT NULL;
```

### DATA-AUTH-001

`external_issuer + external_sub` is the canonical OIDC identity.

### DATA-AUTH-002

Email may be updated without changing a user's identity.

### DATA-AUTH-003

A single external identity cannot map to more than one local user.

## 7. Authentication architecture

Core interface:

```go
type Principal struct {
    UserID uuid.UUID
    Email  string
    Name   string
    Role   Role
}

type Authenticator interface {
    Authenticate(ctx context.Context, req *http.Request) (Principal, error)
}
```

Implementation packages:

```text
internal/auth/oidc
internal/auth/dev
```

HTTP handlers depend on the interface/application service, not directly on an OIDC SDK.

## 8. Production OIDC

Required behavior:

1. validate token signature using issuer metadata/JWKS;
2. validate issuer;
3. validate audience;
4. validate expiration/not-before;
5. extract stable `sub`;
6. map `iss + sub` to local `users`;
7. reject disabled users;
8. update `last_login_at` asynchronously or with a low-contention path.

### SEC-AUTH-001

Never trust role claims from the IdP as application authorization unless explicitly configured and covered by a later spec.

The local `users.role` is authoritative for MVP.

### SEC-AUTH-002

Never identify a user solely by email from an unsigned/unvalidated token.

### SEC-AUTH-003

Unknown external subjects are rejected by default.

Optional auto-provisioning is not part of MVP.

## 9. Development authentication

Development/test may support one of:

- signed local development token;
- explicit mock identity middleware enabled only in development/test.

### SEC-AUTH-004

Development auth must be impossible to enable accidentally in production.

Production startup must fail if a dev-auth bypass is enabled.

### SEC-AUTH-005

A static `admin=true` query parameter/header bypass is forbidden.

## 10. Request authentication middleware

Authenticated API requests populate a request-scoped principal.

Example:

```go
type AuthContext interface {
    Principal(ctx context.Context) (Principal, bool)
}
```

Do not store current user in process-global mutable state.

## 11. Authorization model

Authorization must be explicit at application/API boundaries.

Example:

```go
type Permission string

const (
    PermissionConversationRead      Permission = "conversation.read"
    PermissionConversationReply     Permission = "conversation.reply"
    PermissionConversationReassign  Permission = "conversation.reassign"

    PermissionAnalyticsRead         Permission = "analytics.read"

    PermissionChannelManage         Permission = "channel.manage"
    PermissionWorkflowManage        Permission = "workflow.manage"
    PermissionUserManage            Permission = "user.manage"
    PermissionQueueManage           Permission = "queue.manage"
)
```

Central policy:

```go
type Authorizer interface {
    Allowed(principal Principal, permission Permission) bool
}
```

Resource-scoped checks are added by the owning module.

## 12. Baseline permission matrix

| Capability | Agent | Supervisor | Administrator |
|---|---:|---:|---:|
| View own/allowed conversations | yes | yes | yes |
| Reply to allowed conversation | yes | yes | yes |
| Change status/priority | yes | yes | yes |
| Assign conversation to self | yes | yes | yes |
| Reassign another agent | no | yes | yes |
| View support analytics | limited/no in MVP | yes | yes |
| Manage queues | no | no | yes |
| Manage workflows | no | no | yes |
| Manage channels/credentials | no | no | yes |
| Manage users/roles | no | no | yes |
| View audit log | no | yes (read) | yes |

Queue-specific visibility is implemented in SPEC-100. Until then, the authorization API must allow later resource scopes without breaking contracts.

## 13. Authorization invariants

### FR-AUTH-001

Every endpoint under `/api/v1` is authenticated unless explicitly documented as public infrastructure.

Public foundation endpoints:

```text
/health/live
/health/ready
```

### FR-AUTH-002

Authentication and authorization failures are different:

```text
401 unauthenticated
403 authenticated but forbidden
```

### FR-AUTH-003

The server must not rely only on frontend visibility to enforce permission.

### FR-AUTH-004

An administrator may create/update/disable users.

### FR-AUTH-005

A user may read their own effective profile through `/api/v1/me`.

### FR-AUTH-006

A user cannot elevate their own role unless they already have `user.manage`.

## 14. API

### GET `/api/v1/me`

Response:

```json
{
  "id": "uuid",
  "email": "agent@example.org",
  "name": "Agent Name",
  "role": "agent",
  "status": "active",
  "permissions": [
    "conversation.read",
    "conversation.reply"
  ]
}
```

### GET `/api/v1/users`

Permission:

```text
user.manage
```

Optional filters:

```text
status
role
q
cursor
limit
```

### POST `/api/v1/users`

Permission:

```text
user.manage
```

Request:

```json
{
  "email": "person@example.org",
  "name": "Person",
  "role": "agent",
  "external_issuer": "https://idp.example.org",
  "external_sub": "opaque-subject"
}
```

### PATCH `/api/v1/users/{id}`

Allowed fields:

```text
name
email
role
status
external_issuer/external_sub as an atomic pair
```

Changing identity mapping must create an audit event in the later audit subsystem; until SPEC-170 it must at least create a structured security log.

## 15. API error codes

At minimum:

```text
unauthenticated
forbidden
user_not_found
user_disabled
identity_already_mapped
email_already_exists
invalid_role
invalid_status
validation_failed
```

## 16. User service

Suggested application interface:

```go
type UserService interface {
    GetMe(ctx context.Context, principal Principal) (User, error)
    List(ctx context.Context, principal Principal, filter UserFilter) (Page[User], error)
    Create(ctx context.Context, principal Principal, input CreateUser) (User, error)
    Update(ctx context.Context, principal Principal, id uuid.UUID, input UpdateUser) (User, error)
}
```

Authorization occurs before data mutation.

## 17. Concurrency

### DATA-AUTH-004

Creating two users concurrently with the same external identity must produce exactly one successful mapping.

Database uniqueness is the final guard.

### DATA-AUTH-005

Creating users with email differing only by case must not produce two users.

## 18. Token/session behavior

The implementation may use browser session cookies or validated bearer tokens, but the production choice must be documented in an ADR.

If cookies are used:

- HttpOnly;
- Secure in production;
- SameSite appropriate to deployment;
- CSRF protection for state-changing requests.

If bearer tokens are used:

- never persist token in application logs;
- frontend token storage choice must be reviewed;
- CORS must remain deny-by-default.

## 19. Security requirements

### SEC-AUTH-006

JWKS refresh must handle key rotation without accepting unknown/unverified signatures.

### SEC-AUTH-007

Clock-skew tolerance must be bounded and configured.

### SEC-AUTH-008

Authentication errors must not expose token contents.

### SEC-AUTH-009

Sensitive auth headers must be redacted from traces.

### SEC-AUTH-010

Disabled users must lose API access on the next request even if the external identity token is otherwise valid, subject only to a very short bounded local cache if one is later introduced.

### SEC-AUTH-011

No endpoint may accept `role`, `user_id` or similar client-supplied authorization context as proof of permission.

## 20. Observability

Counters:

```text
auth_requests_total{result}
auth_failures_total{reason}
authorization_denied_total{permission}
user_admin_changes_total{operation}
```

Structured security logs for:

```text
authentication_failed
disabled_user_access
authorization_denied
user_created
user_role_changed
user_disabled
identity_mapping_changed
```

Never log full bearer tokens.

## 21. Acceptance criteria

### AC-AUTH-001

A valid active OIDC-mapped user can call `/api/v1/me`.

### AC-AUTH-002

Invalid signature -> 401.

### AC-AUTH-003

Wrong issuer -> 401.

### AC-AUTH-004

Wrong audience -> 401.

### AC-AUTH-005

Expired token -> 401.

### AC-AUTH-006

Valid token mapped to disabled local user -> 401 or a stable disabled-account response that does not grant API access.

### AC-AUTH-007

Unknown subject -> 401 by default.

### AC-AUTH-008

Agent calling `POST /api/v1/users` -> 403.

### AC-AUTH-009

Administrator can create an agent.

### AC-AUTH-010

Administrator can promote/demote another user.

### AC-AUTH-011

Concurrent create with the same OIDC subject cannot produce duplicate mappings.

### AC-AUTH-012

`Agent@example.org` and `agent@example.org` cannot become two different users.

### AC-AUTH-013

Production refuses to start with development authentication bypass enabled.

### AC-AUTH-014

Captured logs/traces contain neither bearer tokens nor OIDC raw token payloads.

## 22. Mandatory tests

### Unit

- permission matrix;
- role parsing;
- status parsing;
- auth error mapping;
- production config rejects dev auth.

### Integration

- user schema uniqueness;
- create/update/disable user;
- case-insensitive email uniqueness;
- concurrent identity mapping.

### OIDC contract

Use a local test issuer/JWKS:

- valid token;
- rotated signing key;
- wrong issuer;
- wrong audience;
- expired;
- not-before;
- unknown key.

### HTTP

- public health remains public;
- protected endpoint without auth -> 401;
- valid auth + forbidden permission -> 403;
- admin user management flows.

## 23. Task decomposition

```text
010-01 users migration
010-02 User repository
010-03 roles + permission catalog
010-04 Authorizer implementation
010-05 Principal/request context
010-06 OIDC authenticator
010-07 dev authenticator with production guard
010-08 auth middleware
010-09 /api/v1/me
010-10 user management API
010-11 security logging/metrics
010-12 OpenAPI
010-13 concurrency/integration tests
010-14 OIDC contract tests
```

## 24. Codex implementation task

```text
Implement SPEC-010 Authentication, Users and RBAC.

Constraints:
- Production authentication is OIDC-first.
- Local users are pre-provisioned in MVP; unknown OIDC subjects are rejected.
- issuer + sub is the stable identity, not email.
- Local application role is authoritative.
- Authorization is enforced server-side through explicit permissions.
- Do not store a current user in global mutable state.
- Development authentication must fail closed in production.
- Never log bearer tokens or raw token payloads.

Implement the SQL schema, repositories, authenticator abstraction,
OIDC implementation, development implementation, middleware, permission policy,
GET /api/v1/me and user administration endpoints.

Required verification:
- permission matrix tests
- OIDC signature/issuer/audience/expiry tests
- disabled user test
- duplicate external identity concurrency test
- case-insensitive email uniqueness test
- production dev-auth guard test
- logs contain no token
- OpenAPI updated

Before coding, report any security or contract ambiguity instead of inventing
silent behavior.
```
