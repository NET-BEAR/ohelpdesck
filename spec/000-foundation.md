# SPEC-000 Foundation

Status: done  
Priority: P0  
Owner: platform  
Depends on: none

## 1. Goal

Create a reproducible repository and runtime foundation for a Go + React modular-monolith support platform.

At the end of this spec, the system has no support-domain business features, but:

- API and worker processes build and start;
- PostgreSQL, Redis and S3-compatible storage are available locally;
- database migrations work;
- configuration is validated;
- OpenAPI exists;
- health endpoints distinguish liveness and readiness;
- CI provides a trustworthy baseline;
- logging, tracing hooks and metrics infrastructure are present;
- future specs can be implemented without restructuring the repository.

## 2. Non-goals

This spec does not implement:

- users/authentication;
- contacts;
- conversations;
- messages;
- channel adapters;
- jobs/business outbox;
- workflow;
- SLA;
- analytics;
- AI.

It may define infrastructure interfaces that later specs implement.

## 3. Architecture decisions

### ADR-FND-001 Modular monolith

Runtime processes:

```text
support-api
support-worker
support-web
```

`support-api` and `support-worker` share the same internal Go modules and the same PostgreSQL database.

They are separate binaries for deployment and scaling, not separate services with separate ownership or network APIs.

### ADR-FND-002 Data stores

Mandatory:

```text
PostgreSQL        durable source of truth
Redis             cache/pubsub/ephemeral coordination
S3-compatible     attachments/blob storage
```

Redis must not be required to reconstruct durable business state.

### ADR-FND-003 Database access

Preferred:

```text
pgx + sqlc
```

Allowed alternative:

```text
pgx + explicit handwritten repository
```

A high-level ORM with implicit callbacks/lifecycle hooks must not be introduced without an ADR.

### ADR-FND-004 HTTP

Use Go standard `net/http` with a lightweight router such as `chi`.

The router choice must not leak into domain/application packages.

### ADR-FND-005 Frontend

```text
React
TypeScript
Vite
React Router
TanStack Query
```

The UI communicates with the backend through `/api/v1` and `/ws` contracts.

### ADR-FND-006 API contract

OpenAPI 3.1 file:

```text
/api/openapi.yaml
```

is the source of truth for HTTP request/response models.

Generated code may be used, but generated models must not become the domain model.

## 4. Repository structure

Required:

```text
/
├── AGENTS.md
├── README.md
├── Makefile
├── go.mod
├── go.sum
│
├── api/
│   └── openapi.yaml
│
├── cmd/
│   ├── api/
│   │   └── main.go
│   └── worker/
│       └── main.go
│
├── internal/
│   ├── platform/
│   │   ├── config/
│   │   ├── database/
│   │   ├── logging/
│   │   ├── telemetry/
│   │   ├── httpserver/
│   │   ├── redis/
│   │   └── storage/
│   └── ...
│
├── db/
│   ├── migrations/
│   └── queries/
│
├── web/
│
├── tests/
│   ├── integration/
│   └── e2e/
│
├── docs/
│   ├── architecture/
│   └── adr/
│
├── deploy/
│
└── spec/
```

## 5. Build and developer commands

The repository must expose stable commands through `Makefile`.

Minimum:

```bash
make bootstrap
make dev
make stop

make api
make worker
make web

make test
make test-unit
make test-integration
make test-race

make lint
make fmt-check

make migrate-up
make migrate-down
make migrate-status

make generate
make openapi-check
```

### FR-FND-001

`make dev` must start the complete local dependency stack and application in a documented way.

### FR-FND-002

A new developer must be able to start the project without installing PostgreSQL, Redis or MinIO directly on the host.

## 6. Local environment

Required container dependencies:

```text
postgres
redis
minio
```

Application processes may run either on the host or in containers, but one canonical documented flow must exist.

No production credentials may be present in Docker Compose.

## 7. Configuration model

Configuration is loaded once during process startup into an immutable typed structure.

Example:

```go
type Config struct {
    Environment string

    HTTP struct {
        Address         string
        ShutdownTimeout time.Duration
        ReadTimeout     time.Duration
        WriteTimeout    time.Duration
        IdleTimeout     time.Duration
    }

    Database struct {
        URL             string
        MaxOpenConns    int32
        MaxIdleConns    int32
        ConnMaxLifetime time.Duration
    }

    Redis struct {
        URL string
    }

    ObjectStorage struct {
        Endpoint  string
        Region    string
        Bucket    string
        AccessKey string
        SecretKey string
        UseSSL    bool
    }

    Telemetry struct {
        ServiceName  string
        OTLPEndpoint string
    }
}
```

### FR-FND-003

Missing required production configuration must fail startup with a clear non-secret error.

### SEC-FND-001

Secrets must have no unsafe default values.

### SEC-FND-002

Config structures that contain secrets must not be formatted with `%+v`, marshalled to logs, or exposed by debug endpoints.

### SEC-FND-003

Logs must provide a redaction mechanism for known secret keys.

## 8. Environment names

Supported:

```text
development
test
staging
production
```

Production-specific security behavior must not depend on string comparisons spread across the codebase. Centralize environment policy in config/platform code.

## 9. Database

PostgreSQL is mandatory.

### FR-FND-004

Application startup must verify connectivity but must not automatically apply production migrations unless deployment explicitly enables that mode.

### DATA-FND-001

Every table created by the application uses UTC `timestamptz` for business timestamps.

### DATA-FND-002

Use UUIDs for externally exposed entity primary keys unless a later spec explicitly chooses another identifier.

### DATA-FND-003

Schema changes are migration-driven. Runtime auto-schema mutation is forbidden.

## 10. Migrations

Required properties:

- ordered;
- deterministic;
- committed to git;
- executable from an empty database;
- suitable for CI;
- explicit rollback when safe.

Migration filenames should use a timestamp or a strictly monotonic sequence.

### NFR-FND-001

A migration that rewrites a large production table must not be introduced without an operational rollout note.

## 11. Database package

Expose a small abstraction around `pgxpool.Pool`.

Responsibilities:

- connection creation;
- readiness ping;
- metrics hooks;
- transaction helper;
- graceful close.

Example transaction interface:

```go
type TxManager interface {
    WithinTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
}
```

Application services own transaction boundaries.

Repositories accept a DB executor/transaction but do not decide when a business transaction starts.

## 12. Redis

Redis is optional for API readiness only if the current enabled features require it.

At foundation stage:

- connection package exists;
- readiness can expose Redis status separately;
- core process may start with Redis unavailable in development if configured accordingly.

Redis later supports:

- realtime pub/sub;
- ephemeral cache;
- distributed rate limit;
- short-lived coordination.

### DATA-FND-004

No durable job or message is stored only in Redis.

## 13. Object storage

Use an S3-compatible client abstraction.

Interface:

```go
type ObjectStore interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
    Health(ctx context.Context) error
}
```

At foundation stage, no business blob type is required.

## 14. HTTP API conventions

Base:

```text
/api/v1
```

All JSON uses UTF-8.

### Request IDs

Accept optional:

```text
X-Request-ID
```

If absent, generate one.

Return it on responses.

### Errors

Canonical shape:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "Request validation failed",
    "details": {
      "field": "reason"
    },
    "request_id": "..."
  }
}
```

### FR-FND-005

API code must use stable machine-readable error codes.

### SEC-FND-004

Internal stack traces, SQL, credentials and provider tokens must never appear in API error responses.

## 15. OpenAPI

Initial `api/openapi.yaml` must include:

```text
GET /health/live
GET /health/ready
```

and standard error schemas.

### FR-FND-006

CI fails when OpenAPI is syntactically invalid.

### FR-FND-007

When request/response generated code is used, `make generate` must be deterministic and CI must detect uncommitted generated changes.

## 16. Health model

### `GET /health/live`

Purpose: determine whether the process is alive.

It must not depend on PostgreSQL, Redis, S3 or any external network dependency.

Response when process event loop is healthy:

```http
200
```

### `GET /health/ready`

Purpose: determine whether the process can safely receive traffic.

Minimum API readiness dependency:

```text
PostgreSQL
```

Response:

```json
{
  "status": "ready",
  "checks": {
    "postgres": "ok",
    "redis": "ok",
    "object_storage": "ok"
  }
}
```

A dependency may be `degraded` when not mandatory for the currently enabled feature set.

### FR-FND-008

PostgreSQL unavailable -> API readiness returns non-2xx.

### FR-FND-009

Liveness remains 200 during a temporary PostgreSQL failure if the process itself is healthy.

## 17. Graceful shutdown

API:

1. stop accepting new connections;
2. allow in-flight requests to complete within configured timeout;
3. close database/redis/telemetry cleanly.

Worker:

1. stop leasing new work;
2. complete or safely abandon currently leased work according to SPEC-030;
3. flush telemetry;
4. exit before deployment termination grace period.

Foundation provides lifecycle primitives even before job implementation.

## 18. Logging

Use structured JSON logging in non-development environments.

Required common fields:

```text
timestamp
level
service
environment
message
request_id
trace_id
```

Contextual fields are added later:

```text
conversation_id
message_id
channel_id
user_id
job_id
```

### SEC-FND-005

Do not log authorization headers, cookies, passwords, API tokens or complete connection strings.

## 19. Telemetry

Provide OpenTelemetry initialization.

Instrument at minimum:

```text
HTTP server
PostgreSQL client
outbound HTTP client wrapper
```

Actual business spans are introduced by later specs.

OpenTelemetry exporter failure must not crash messaging processes.

## 20. Metrics

Expose a Prometheus-compatible metrics endpoint on a deployment-safe internal listener or path.

Foundation metrics:

```text
http_requests_total
http_request_duration_seconds
process_start_time_seconds
db_pool_acquired_connections
db_pool_idle_connections
```

Authentication/protection of metrics endpoint is deployment-specific but must be documented.

## 21. HTTP client policy

All outbound integrations later use a shared client factory.

Defaults must enforce:

- connect/request timeout;
- bounded idle connections;
- TLS verification;
- user-agent;
- trace propagation;
- safe redirect policy.

No module may create an unbounded default HTTP client for production calls.

## 22. Frontend foundation

Required:

```text
React
TypeScript
Vite
React Router
TanStack Query
```

The foundation UI contains:

```text
App shell
Error boundary
API client
Query client
Routing skeleton
Health/development page
```

### NFR-FND-002

Frontend API code must be isolated from presentation components.

### NFR-FND-003

The frontend must support optimistic updates later, but no business behavior is required here.

## 23. CI pipeline

Required checks:

### Backend

```bash
go test ./...
go vet ./...
go test -race ./...
```

Use an additional static analyzer only if pinned and reproducible.

### Frontend

```text
install with lockfile
typecheck
lint
unit tests
build
```

### Database

CI must:

1. create empty database;
2. apply all migrations;
3. run integration tests.

### OpenAPI

Validate schema.

### Generated code

Regenerate and fail if git becomes dirty.

## 24. Dependency policy

- Versions are pinned through native lock/module mechanisms.
- Do not add a dependency when the standard library or already-used dependency solves the problem adequately.
- Every new infrastructure dependency must have an owner and reason in the pull request.
- Avoid channel SDKs that leak provider models into core.

## 25. Security baseline

### SEC-FND-006

Application containers must be able to run as a non-root user.

### SEC-FND-007

Production build must not include development authentication bypasses or debug endpoints unless explicitly enabled by secure configuration.

### SEC-FND-008

CORS is deny-by-default and configured by allowed origins.

### SEC-FND-009

Request body size must have a global upper bound; upload flows later may define a separate controlled path.

### SEC-FND-010

HTTP servers must define read/write/header timeouts.

## 26. AGENTS.md bootstrap requirements

SPEC-000 must create `AGENTS.md` with at least:

```text
- modular monolith
- PostgreSQL source of truth
- no provider leakage into core
- spec -> tests -> implementation lifecycle
- explicit transactions
- no silently ignored errors
- every external call has timeout
- do not log secrets
- new dependencies require justification
- update OpenAPI when HTTP contract changes
```

## 27. Acceptance criteria

### AC-FND-001

From a clean checkout:

```bash
make bootstrap
make dev
```

starts local dependencies and documented application processes.

### AC-FND-002

`support-api` starts with valid configuration and returns 200 from `/health/live`.

### AC-FND-003

With PostgreSQL healthy, `/health/ready` reports ready.

### AC-FND-004

With PostgreSQL stopped, `/health/ready` becomes non-ready while `/health/live` remains live.

### AC-FND-005

`support-worker` starts and shuts down gracefully.

### AC-FND-006

All migrations apply to an empty PostgreSQL database.

### AC-FND-007

`make test` passes on a clean checkout.

### AC-FND-008

CI rejects invalid OpenAPI.

### AC-FND-009

CI detects generated-code drift if generation is introduced.

### AC-FND-010

A configured secret value used in tests cannot be found in captured structured logs.

### AC-FND-011

Frontend production build succeeds and uses the shared API client layer.

### AC-FND-012

No support-domain table exists yet except generic migration bookkeeping.

## 28. Mandatory tests

### Unit

- configuration parsing;
- required config validation;
- secret redaction;
- canonical error mapping.

### Integration

- PostgreSQL connect/ping;
- migration from empty DB;
- Redis connect behavior;
- MinIO object put/get/delete through abstraction;
- readiness dependency behavior.

### Process

- graceful API shutdown;
- graceful worker shutdown.

## 29. Task decomposition

```text
000-01 repository skeleton
000-02 Go module and API binary
000-03 worker binary and lifecycle
000-04 typed configuration
000-05 PostgreSQL pool + migrations
000-06 Redis package
000-07 S3/MinIO abstraction
000-08 HTTP conventions + error model
000-09 health endpoints
000-10 logging + redaction
000-11 OpenTelemetry + metrics
000-12 React/Vite foundation
000-13 Docker Compose
000-14 Makefile
000-15 CI pipeline
000-16 AGENTS.md
000-17 integration verification
```

## 30. Codex implementation task

```text
Implement SPEC-000 Foundation exactly as written.

Goal:
Create the reproducible technical foundation for a Go + React modular-monolith
support platform.

Important constraints:
- Do not implement support-domain business features.
- PostgreSQL is the durable source of truth.
- Redis is never the only durable store.
- Prefer pgx + sqlc or an explicit thin repository layer.
- Use explicit transaction ownership.
- OpenAPI is the HTTP contract.
- No secrets in logs or API errors.
- All outbound HTTP clients must have timeouts.
- The application must support graceful shutdown.
- The repository must contain AGENTS.md with the architecture rules from the spec.

Required verification:
- clean bootstrap
- migrations from empty database
- API liveness/readiness behavior
- worker startup/shutdown
- backend tests
- race tests
- frontend typecheck/tests/build
- OpenAPI validation
- secret-redaction regression test

Before coding:
1. inspect the current repository;
2. produce a short task plan mapped to 000-01..000-17;
3. identify any conflict with this spec;
4. do not change architecture to microservices.

After coding:
1. run all required checks;
2. summarize changed files;
3. list remaining risks;
4. do not mark the spec complete if any acceptance criterion is unverified.
```
