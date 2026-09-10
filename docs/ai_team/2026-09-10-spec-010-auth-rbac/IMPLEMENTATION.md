# Реализация SPEC-010: local password slice

Статус: `ready_for_review`. Дата: 2026-09-10.

## Изменения

| Область | Реализация |
|---|---|
| Schema | `000002_auth_users_rbac` создаёт users, bundles, assignments и sessions; migration runner применяет ordered checksummed source и защищён advisory lock |
| Identity | `internal/auth` хранит Argon2id hashes, сравнивает constant-time, выдаёт opaque random session/CSRF secrets и хранит их SHA-256 hashes |
| Authorization | fixed permission catalog plus DB bundles; API reads effective permissions from server; `user.manage`/`role.manage` не доверяются input/UI |
| API | local login/logout, `/me`, user CRUD subset, permission-bundle list/create/assignment; health remains public |
| Bootstrap | `cmd/bootstrap-admin` creates `sysadmin` once from `INITIAL_ADMIN_PASSWORD` runtime secret; it emits no password |
| Web | `/login` posts credentials only once; cookie remains HttpOnly; CSRF is module-memory only; `/me` displays server result |

## Verification snapshot

- RED: missing auth symbols produced compilation failure in `internal/auth` tests.
- `make test-integration`: exit 0, 83.39% Go statements; 80.02% executable block lines (909/1136).
- frontend: `npm run typecheck`, 30 Vitest tests and production build: exit 0; 93.22% lines.
- OpenAPI validator: positive valid and deliberate negative control rejected.
- `go test -race -count=1 ./...`: exit 0.
- `govulncheck`: exit 0, 0 reachable vulnerabilities; 2 imported-package and 18 module advisories not reached by project code.

Pending independent reviewer and QA determine acceptance, not this implementation report.
