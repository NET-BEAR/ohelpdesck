# Реализация SPEC-010: Authentication, Users, RBAC

Статус задачи: `in_review`. Дата: 2026-09-10. Владелец: оркестратор.

## Цель и границы

Реализовать OIDC-first вход для внутренних операторов, локальные pre-provisioned users, server-side RBAC и подтверждённые проектные решения: invitation продолжает OIDC, а effective permissions состоят из системной роли и permission bundles. В scope входят API, PostgreSQL schema/migrations, backend, OpenAPI, frontend login/admin contracts и тесты. Провайдеры каналов, customer login, SCIM, auto-provisioning, passwordless session и production DNS/TLS вне scope.

Переданная строка с учётными данными не считается OIDC-конфигурацией и не включается в документы, git, логи или тестовые fixtures.

## Подтверждённые решения

1. До готовности корпоративного Keycloak применяется `local_password`: предварительно созданные пользователи входят по login/password, session хранится на сервере; HttpOnly cookie и CSRF обязательны.
2. Future OIDC Authorization Code + PKCE остаётся отдельным adapter. До его включения invitations не создают OIDC context, callback или passwordless session и потому исключены из текущего среза.
3. Системные роли `agent`, `supervisor`, `administrator` сохранены. Сервер вычисляет effective permissions из фиксированного versioned catalog, роли и назначенных bundles; UI не является источником авторизации.
4. Первый local administrator — login `sysadmin`; его password берётся только из runtime secret storage при bootstrap, сразу превращается в Argon2id hash и никогда не попадает в БД, git, документацию или логи в исходном виде.

## Решение о временной аутентификации

Корпоративный Keycloak будет подключён позднее. Пользователь подтвердил 2026-09-10 вариант A: начать без него, с авторизацией внутри системы. Это фундаментальное изменение исходного production OIDC-first contract `SPEC-010`.

| Вариант | Последствия | Рекомендация |
|---|---|---|
| A. Внутренний password login как временный provider | Пользователи и bootstrap admin входят по login/password; password только Argon2id hash, server session/CSRF; в production режим разрешён явной конфигурацией до миграции на Keycloak | **Подтверждено пользователем.** Позволяет сделать и проверить полный admin flow сейчас, изолируя provider за `Authenticator` interface |
| B. Только development mock/signed identity | Не нужны password hashes; подходит для API/QA, но реальные операторы не могут войти | Не покрывает запрос на авторизацию внутри системы |
| C. Остановиться до Keycloak | Сохраняет изначальный OIDC-first contract | Блокирует реализацию и проверку login flow |

При A будущая миграция не переписывает users/RBAC/API: добавляется OIDC authenticator, затем policy отключает local password login после контролируемой миграции. Password reset, recovery, signup, social login и передачу паролей в документацию/CI по-прежнему не реализуем.

## Impact analysis

Класс: фундаментальное изменение. Самая ранняя затронутая точка — `BUSINESS_ANALYSIS.md`: изменяется доказательство личности и lifecycle пользователя. Обновлены `ARCHITECTURE.md`, `ACCEPTANCE_TESTS.md`, `UX_DESIGN.md` и `DECISIONS.md`; исходный OIDC invitation contract из UI-задачи помечен superseded только для первого runtime-среза. После кода обязательны новый независимый security review и QA authentication/regression.

## Отложенные входные данные

Для будущего OIDC callback потребуются issuer discovery URL, audience/client ID и redirect URI. Это не блокирует local-password срез. Password `sysadmin` будет внесён в существующее runtime secret storage только во время отдельного dev deploy; текущая разработка и тесты используют изолированные fixtures, не значение из переписки.

## Последовательность

1. Зафиксировать временный provider и bootstrap mapping; обновить решения, SPEC-010 и configuration contract.
2. Подготовить migration, domain/application API, password/session/auth middleware, authorizer и admin endpoints; сначала RED acceptance tests.
3. Реализовать минимальные вертикали: `/api/v1/me`, login/logout, user lifecycle и effective permissions.
5. Обновить OpenAPI, UI login/admin client contracts, metrics/security logs и CI.
6. Провести независимые code review и QA, включая local test issuer/JWKS rotation, callback replay, disabled user и конкурентные invite/user flows.

## Реализация и повторная передача на review

Реализованы ordered/checksummed migrations до `users`, permission bundles, assignments и server-side sessions; `internal/auth` с Argon2id, opaque session/CSRF, rate limiter, local login/logout, `/api/v1/me`, user/bundle administration и last-active-administrator guard. `bootstrap-admin` создаёт единственный initial `sysadmin` только из runtime `INITIAL_ADMIN_PASSWORD`; source/runtime logs не получают raw password. HTTP contract и React login/profile client обновлены.

Подтверждён RED: до `internal/auth` новые auth tests не компилировались. GREEN: Go integration tests прошли, `make test-integration` — 83.39% statements и 80.02% executable block lines. После первоначального независимого review выявлены P1/P2, поэтому первоначальный implementation result считается `superseded` для security-rework.

### Security rework по REVIEW.md

Минимальная точка возврата: repository transaction, login limiter/verification, telemetry и затронутые acceptance tests. Исправлено:

1. `Repository.Update` получает transaction-scoped PostgreSQL advisory lock перед чтением роли и статуса. Это сериализует параллельные transitions и не позволяет двум транзакциям снять двух последних active administrators одновременно.
2. Limiter ключуется SHA-256 от нормализованного login, а не `RemoteAddr`; nginx больше не может объединить всех клиентов в один лимит. Ключ и исходный login не логируются.
3. Для неизвестного или disabled пользователя выполняется Argon2id comparison с runtime-only dummy hash; HTTP result по-прежнему нейтрален.
4. Добавлены bounded-label Prometheus counters `auth_requests_total`, `auth_failures_total`, `authorization_denied_total`, `user_admin_changes_total` и structured security events. События не содержат login, password, cookie или CSRF.

Выполнены targeted checks: `go test ./internal/auth ./internal/platform/telemetry ./internal/platform/runtime` и `go test ./tests/integration` — exit 0. Новый integration test запускает два concurrent disable двух administrators и подтверждает ровно одну successful mutation и одного оставшегося active administrator; отдельные capture tests проверяют metrics и отсутствие credentials/login в security logs. Далее обязательны полный verify, повторный независимый review и QA.

## Артефакты

| Артефакт | Назначение | Статус |
|---|---|---|
| `RND.md` | подтверждённые факты, разрывы и вопросы | approved |
| `BUSINESS_ANALYSIS.md` | пользователи, правила и сценарии | approved |
| `ARCHITECTURE.md` | target contracts и security boundaries | approved |
| `ACCEPTANCE_TESTS.md` | Given/When/Then до кода | approved |
| `UX_DESIGN.md` | используемый UI-contract из отдельной утверждённой задачи | approved |

Выбранные docs-driven artifacts разрешают реализацию. Для local-password среза не требуется выдумывать IdP параметры.

## Выбранные skills

Роль: оркестратор/разработчик. Компетенция: безопасная поставка по approved docs и acceptance-driven implementation. Выбраны `capability-routing` и `docs-driven-delivery` из локального approved registry; первый зафиксировал минимальный маршрут, второй требует RED → GREEN → независимые review/QA. Кандидатные внешние security/OIDC skills отклонены: не нужны для local-password среза, установка не разрешена. Fallback — встроенные Go криптографические пакеты и существующий containerized test workflow.
