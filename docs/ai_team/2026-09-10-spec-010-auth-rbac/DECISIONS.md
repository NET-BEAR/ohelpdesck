# Решения SPEC-010

Статус: `approved`. Дата: 2026-09-10.

1. Пользователь подтвердил временный internal password login до готовности корпоративного Keycloak. `AUTH_PROVIDER=local_password` становится единственным provider первого среза.
2. Login bootstrap administrator — `sysadmin`. Пароль не копируется из переписки; deploy записывает его только в existing runtime secret storage, а application сохраняет только Argon2id hash.
3. OIDC invitation continuation из ADR-UI-001 superseded только для текущего authentication runtime-slice. Future Keycloak adapter получает отдельный decision/refinement; local login не выдаёт invitations и не создаёт passwordless sessions.
4. Для local provider принят server-side opaque session: HttpOnly cookie, Secure в production, CSRF validation на mutation, session rotation on login, hash at rest and expiry/revocation in PostgreSQL.
5. Permission bundles остаются в scope: versioned fixed catalog, server-computed effective access, protected administration, no self-escalation and last-active-administrator invariant.
