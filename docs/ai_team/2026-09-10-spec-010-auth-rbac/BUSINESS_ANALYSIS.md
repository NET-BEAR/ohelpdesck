# Бизнес-правила SPEC-010

Статус: `ready_for_review`.

## Роли и сценарии

Администратор создаёт локального пользователя, назначает системную роль и допустимые permission bundles, затем безопасно передаёт initial password вне приложения. Пользователь входит по login/password. Сервер создаёт session и применяет вычисленные server-side permissions. Корпоративный Keycloak и OIDC invitation flow подключаются последующим срезом.

Пользователь может читать собственный effective profile. Agent не управляет пользователями. Administrator не может отключить последнего active administrator или снять его последнюю administrative capability. Disabled user теряет доступ на следующем запросе. Unknown OIDC subject не становится пользователем автоматически.

## Минимальная модель

`users` хранит immutable local ID, unique login, email/name, Argon2id password hash, system role и status. `permission_catalog` является кодовым, versioned catalog. `permission_bundles` и assignments хранятся в PostgreSQL и изменяются только пользователем с server-validated administrative permission. OIDC external identity и invitations — future tables, не часть этого deployable slice.

## Обязательные outcomes

| Сценарий | Outcome |
|---|---|
| Активный mapped OIDC user | `GET /api/v1/me` показывает effective role/permissions |
| Нет session/invalid token | 401 с нейтральным error code без token contents |
| Authenticated agent вызывает user admin API | 403, данных и изменений нет |
| Local user вводит правильный login/password | получает session и CSRF token; raw password нигде не сохраняется |
| Неверный login/password или disabled user | нейтральный отказ, session не создаётся |
| Admin создаёт/обновляет пользователя | password может быть задан только через отдельный protected command; server сохраняет Argon2id hash |
