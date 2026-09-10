# QA/ПСИ: SPEC-010 local_password

Статус: `needs_rework`. Дата: 2026-09-10. Проверенный commit: `8c6f706`.

## Граница и окружение

Проведена независимая QA-проверка local-password среза: API/login/session/CSRF,
RBAC users и bundles, invariant последнего администратора, observability,
migration/OpenAPI и React-клиент. Проверялись исходный код, acceptance tests,
implementation и финальный independent security review. Memory MCP, `.env`,
секреты и удалённый dev-контур не использовались.

Локальный host не содержит `go` и `npm`; проверки выполнены в чистых Docker
образах `golang:1.25.13-bookworm`, `node:22.22.0-bookworm-slim` и
`python:3.12.12-slim`. Контейнерный Go race-run не получил переменных
integration environment, поэтому tests/integration корректно завершились через
проектный skip и не являются самостоятельным runtime evidence PostgreSQL/Redis/
S3. Фактический integration evidence ниже отделён от независимого review.

## Выполненные проверки и evidence

| Проверка | Результат | Evidence |
|---|---|---|
| Go race/regression | PASS | `docker run --rm -v "$(pwd):/src" -w /src golang:1.25.13-bookworm go test -race -count=1 ./...` — exit 0; `internal/auth` 12.237s, все package tests exit 0. Integration package был skipped из-за отсутствия runtime variables. |
| Web contract, typecheck, lint, build, audit | PASS | Docker Node command: `npm ci && npm run typecheck && npm run lint && npm test -- --run && npm run build && npm audit --audit-level=moderate` — exit 0; 30/30 Vitest, lines 93.22%, audit: 0 vulnerabilities. |
| OpenAPI | PASS | `deploy/validate_openapi.py` с `openapi-spec-validator==0.7.2` — exit 0; schema valid, deliberate negative control rejected. |
| CI/CD delivery regression | PASS | `bash -n deploy/{receive-deploy,remote-apply,smoke}.sh` и `deploy/test_delivery.py` — exit 0; 8/8 tests. |
| Git hygiene | PASS | `git show --check HEAD`, `git diff --check`, `git diff --exit-code -- api/openapi.yaml` — без ошибок и без незакоммиченного OpenAPI diff. Нерелевантные пользовательские изменения `.gitignore`, `docs/ai_team/PLANS.md` и `2026-09-10-chatwoot-inspired-ui/` не изменялись. |
| Независимый security re-review | PASS с ограничением | `REVIEW.md`: verdict `approve`; `make verify` exit 0, Go coverage 84.19% statements / 81.00% executable block lines; `go test -count=3 ./tests/integration` exit 0. Это evidence Reviewer, не повторено QA из-за правила не читать `.env`. |

## Результаты по acceptance criteria

| AC | Результат | Основание |
|---|---|---|
| 1. Correct password, session and `/me` | PASS | `TestLocalPasswordLoginAndAuthorization`; code verifies Argon2id and returns opaque HttpOnly session, profile contains server-calculated permissions. |
| 2. Wrong/missing password and disabled user | PASS | Integration paths assert 401 for wrong password and disabled user; handler uses dummy Argon2id hash for missing/disabled identity. |
| 3. Credentials do not enter observability | PASS | Integration security-log capture rejects password and login values; repo scan found no supplied credential in source/tracked files; logs use bounded event fields. |
| 4. Agent cannot mutate users/bundles | PASS | Integration test receives 403 before user mutation; authorization uses server effective permissions. |
| 5. Admin creates protected password user | PASS | `Repository.Create` uses Argon2id; safe responses omit hash; integration checks profile has no `password_hash`. |
| 6. CSRF before mutation, logout revokes session | PASS | Missing CSRF returns 403; logout is 204 and subsequent `/me` returns 401. |
| 7. Concurrent duplicate login/email creation | **FAIL — QA-010-01** | Case-insensitive DB uniqueness and sequential duplicate conflict are tested, но нет concurrent HTTP/repository test с одинаковыми login/email, который требует AC. Runtime race therefore is not acceptance-confirmed. |
| 8. Last active administrator, including concurrency | PASS | Advisory transaction lock plus integration concurrency test; Reviewer repeated it three times against persistent DB and verified invariant `active administrators >= 1`. |
| 9. Unsupported provider/dev bypass fail closed | PASS | Config tests cover unsupported provider and production policy; startup tests ensure errors do not disclose config input. |
| 10. Repeated invalid login is rate-limited | **PARTIAL — QA-010-02** | Unit test proves isolated limiter count/reset by normalized login, but no handler/integration test performs six wrong-password POSTs and asserts 429/no further verification. The end-to-end AC is unproven. |
| 11. Repeated ordered migration/status | PASS | Checksummed runner and migration tests are present; independent Reviewer recorded successful full verification and integration migrations. |

## Bug reports

### QA-010-01 — отсутствует acceptance test конкурентного создания одинаковых users

Severity: P2. Reproduction: start two concurrent authenticated create requests
with login or email differing only by case; AC expects exactly one `201` and one
`409`, with one persisted user. Current coverage tests only a sequential
duplicate. PostgreSQL unique indexes make the expected implementation likely,
but DB behaviour under concurrent HTTP requests has not been verified as
required by `ACCEPTANCE_TESTS.md` scenario 7.

Required fix: add deterministic integration test with barriers/two concurrent
requests and assert outcome plus persisted cardinality for both login and email
case-insensitive collisions.

### QA-010-02 — отсутствует сквозной test login rate-limit policy

Severity: P2. Reproduction: issue more than five invalid password POSTs for
one normalized login in the configured 15-minute window; sixth request must be
`429` and not execute password verification. Current unit test covers
`loginLimiter`, not handler wiring, authentication failure path, response
contract or telemetry.

Required fix: add handler/integration scenario with injected/verifiable
password verifier or an observable repository seam. Assert five neutral `401`,
then `429`, `auth_failures_total{reason="rate_limited"}` increment and
isolation for a different login.

## Материалы ПСИ для повторного прогона

1. Поднять isolated dev stack with generated test-only users; run `migrate up`
twice and `migrate status`.
2. Войти корректным password; проверить HttpOnly/SameSite cookie, `/api/v1/me`,
отсутствие password/hash в body и browser memory-only CSRF.
3. Проверить wrong, missing и disabled identity; убедиться в нейтральном 401 и
отсутствии session cookie.
4. Выполнить state-changing API без/wrong CSRF, затем logout и повторный `/me`.
5. Проверить agent 403 на user/role administration без изменения БД; проверить
administrator create/update/bundle assignment.
6. Выполнить параллельные duplicate user creates (login и email) и параллельное
отключение active administrators; сверить database invariant.
7. Выполнить шесть failed login для одного login и вход для другого; сверить
429, metrics and absence of credentials from structured logs.

## Verdict

`needs_rework`: два P2 не являются подтверждёнными acceptance scenarios.
Production security findings из `REVIEW.md` закрыты, однако до ПСИ и deploy
нужно добавить QA-010-01 и QA-010-02, выполнить их на isolated integration
environment, затем повторить independent review (затронуты tests/observability)
и QA retest.
