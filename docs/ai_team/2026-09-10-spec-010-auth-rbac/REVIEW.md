# Независимый security review: SPEC-010 local_password

Статус: `needs_changes`. Дата: 2026-09-10. Проверяющий: независимый Reviewer.

## Scope и evidence

Проверены текущие незакоммиченные изменения SPEC-010: SQL migration, `internal/auth`, platform/config/http/runtime, bootstrap, compose/deploy, OpenAPI, frontend-контракт и tests. Секреты и `.env` не читались.

Выполнено без ошибок:

- `docker compose --env-file .env -f deploy/compose.yml run --rm go go test -race -count=1 ./internal/auth ./internal/platform/config ./internal/platform/httpserver ./cmd/bootstrap-admin ./cmd/migrate` — exit 0;
- `docker compose --env-file .env -f deploy/compose.yml run --rm go go test -count=1 ./tests/integration` — exit 0;
- `docker compose --env-file .env -f deploy/compose.yml run --rm go go vet ./...` — exit 0;
- OpenAPI validator и его deliberate negative control — exit 0;
- `git diff --check` — без whitespace errors.

Тесты подтверждают базовый вход, opaque hash-at-rest session, CSRF, RBAC для обычного agent, Argon2id format и migration path. Они не покрывают конкурентное снятие прав последнего администратора, реальный reverse-proxy key rate limiter, security observability и равномерную проверку неизвестного login.

## Findings

### P1 — параллельное отключение двух администраторов нарушает инвариант последнего active administrator

В [repository.go](/Users/krassus/github/ohelpdesck/internal/auth/repository.go:102) блокируется только изменяемая строка пользователя. Проверка количества active administrators выполняется отдельным обычным `SELECT count(*)` на [строке 132](/Users/krassus/github/ohelpdesck/internal/auth/repository.go:132). При двух администраторах два параллельных `PATCH` могут каждый увидеть `count=2`, затем отключить разные строки и закоммититься. В migration нет database-level guard или сериализации всех active administrator rows: [000002_auth_users_rbac.up.sql](/Users/krassus/github/ohelpdesck/db/migrations/000002_auth_users_rbac.up.sql:10).

Это нарушает AC сценарий и BUSINESS_ANALYSIS: система может остаться без активного администратора. Нужна сериализация этой операции — например, транзакционный advisory lock для admin-role transitions либо блокировка набора active administrator rows до подсчёта и update — и integration test с двумя конкурентными попытками disable/demote.

### P1 — rate limit входа становится общим для всех клиентов за nginx

Login limiter ключует попытки по `r.RemoteAddr` на [http.go](/Users/krassus/github/ohelpdesck/internal/auth/http.go:184), а `limiterKey` использует host peer connection на [строках 323–329](/Users/krassus/github/ohelpdesck/internal/auth/http.go:323). В production delivery весь трафик к API проходит через nginx `proxy_pass` ([nginx.conf](/Users/krassus/github/ohelpdesck/deploy/nginx.conf:17)), который не передаёт и API не валидирует адрес исходного клиента. Следовательно, API увидит один адрес контейнера `web`; пять ошибочных попыток одного пользователя заблокируют вход всем пользователям на 15 минут.

Нужна явная trust-boundary policy: либо безопасно передавать/разбирать client IP только от trusted proxy, либо устанавливать rate-limit key по нормализованному login вместе с proxy-verified IP. Добавить integration test через реальный nginx, демонстрирующий изоляцию лимита для двух клиентов и невозможность доверять произвольному forwarded header.

### P2 — неизвестный login не проходит Argon2id verification и создаёт timing oracle

В [login](/Users/krassus/github/ohelpdesck/internal/auth/http.go:192) короткое замыкание `err != nil || ... || VerifyPassword(...)` возвращает 401 для отсутствующего login до Argon2id. Для существующего пользователя выполняется password hash с memory cost 64 MiB ([password.go](/Users/krassus/github/ohelpdesck/internal/auth/password.go:13)). HTTP body одинаков, но стабильная разница времени позволяет различать существующие логины при достаточно большом числе измерений.

Следует выполнять constant-work verify с фиксированным валидным dummy Argon2id hash при `ErrNotFound`, сохраняя нейтральный response и existing rate-limit. Добавить test на обязательный вызов verifier в absent-user branch через injectible verifier или другой наблюдаемый seam.

### P2 — обязательная security observability из SPEC-010 не реализована и не тестируется

SPEC требует `auth_requests_total`, `auth_failures_total`, `authorization_denied_total`, `user_admin_changes_total` и structured security events ([spec/010-auth-users-rbac.md](/Users/krassus/github/ohelpdesck/spec/010-auth-users-rbac.md:475)). В login/authz/user mutation routes нет ни metrics, ни security log calls: [http.go](/Users/krassus/github/ohelpdesck/internal/auth/http.go:183), [http.go](/Users/krassus/github/ohelpdesck/internal/auth/http.go:228), [http.go](/Users/krassus/github/ohelpdesck/internal/auth/http.go:146). Общий access log не заменяет требуемые outcome/reason/permission/operation signals.

Нужно добавить bounded-label metrics и security logs без password, cookie, CSRF и login secret values; затем добавить capture tests на successful/failed/disabled login, denied authorization и user mutation. Это закрывает task decomposition 010-11 и AC о non-leak observability.

## Положительные наблюдения

- Пароли имеют минимальную длину, хешируются Argon2id с 16-byte random salt, фиксированными допустимыми parameters и constant-time comparison ([password.go](/Users/krassus/github/ohelpdesck/internal/auth/password.go:15)).
- Session и CSRF values генерируются криптографически, хранятся как hashes, а raw values не входят в user response ([session.go](/Users/krassus/github/ohelpdesck/internal/auth/session.go:24), [http.go](/Users/krassus/github/ohelpdesck/internal/auth/http.go:253)). Cookie содержит `HttpOnly`, `SameSite=Lax` и `Secure` в production ([http.go](/Users/krassus/github/ohelpdesck/internal/auth/http.go:246)).
- DB uniqueness для login/email case-insensitive и permission bundle assignment transaction уже есть ([000002_auth_users_rbac.up.sql](/Users/krassus/github/ohelpdesck/db/migrations/000002_auth_users_rbac.up.sql:24)).
- `AUTH_PROVIDER` проходит fail-closed validation, а raw configuration не выводится в API startup error ([config.go](/Users/krassus/github/ohelpdesck/internal/platform/config/config.go:34), [cmd/api/main.go](/Users/krassus/github/ohelpdesck/cmd/api/main.go:18)).

## Verdict

`needs_changes`.

До QA и deploy должны быть устранены оба P1; затем повторить независимый review и security-focused integration tests. P2 должны быть закрыты в этом P0 security slice, поскольку они соответствуют явно перечисленным требованиям SPEC-010.
