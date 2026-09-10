# RND: вход, приглашения и RBAC для SPEC-010

Статус: `ready_for_review`. Источники: `spec/010-auth-users-rbac.md`, foundation checkout, утверждённые ADR-UI-001/002 из `2026-09-10-chatwoot-inspired-ui`.

## Подтверждённое в checkout

- Foundation содержит только health endpoints. `/api/v1` router, users schema, auth middleware, session store и OIDC client отсутствуют.
- `config.Config` читает только foundation settings; нет issuer, client/audience, callback, cookie/CSRF или JWKS policy.
- Мигратор применяет только `000001_foundation`; migration runner пока не является versioned sequence.
- `SPEC-010` требует validated signature, issuer, audience, expiry/not-before, local mapping by `issuer + sub`, reject disabled/unknown user, explicit permission policy and no token logging.
- Проектная UI-задача зафиксировала OIDC invitation continuation и permission bundles. Это расширяет исходный SPEC-010, который исключал fine-grained roles; необходим точный refinement, а не молчаливое расхождение.

## Контрактные разрывы

| ID | Что отсутствует | Риск | Безопасный шаг |
|---|---|---|---|
| AUTH-01 | IdP issuer discovery URL, client ID/audience, approved redirect URI | нельзя валидировать `iss`, `aud` или callback | отложить до подключения корпоративного Keycloak; не подставлять фиктивные значения |
| AUTH-02 | Первый active administrator: `issuer + sub`, name/email или controlled invitation owner | нельзя безопасно bootstrap `user.manage` | для local-password режима: email/login + initial password через runtime secret storage |
| AUTH-03 | Client type и login topology | session/CSRF/cookie policy различаются | default: confidential server client + code/PKCE; подтвердить совместимость IdP |
| AUTH-04 | Exact bundle catalog и default assignments | риск самоповышения и несогласованных прав | зафиксировать минимальный catalog и last-admin invariant до migration |
| AUTH-05 | Invitation delivery/redirect policy | token leakage/replay/open redirect | hashed single-use token, TTL/revoke/rate limit, allowlist return URLs |
| AUTH-06 | Revision of migration runner | нельзя применять users/invites schema над foundation корректно | сделать ordered, checksummed, forward-only migrations перед бизнес-таблицами |
| AUTH-07 | Policy внутреннего password login до Keycloak | риск случайно оставить временный provider без контроля | **Решено пользователем:** explicit `AUTH_PROVIDER=local_password`; future OIDC adapter keeps same Authenticator boundary |

## Обращение с переданными учётными данными

Строка пользователя была паролем системной учётной записи. Её нельзя проверять на угадываемых хостах, сохранять или использовать как OIDC input. Local-password режим подтверждён; bootstrap secret передаётся только в runtime secret storage при deploy, не в git, документацию или CI logs.
