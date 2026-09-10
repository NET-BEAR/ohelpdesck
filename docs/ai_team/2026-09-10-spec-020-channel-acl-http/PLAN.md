# SPEC-020: Channel ACL и HTTP boundary

Статус: `planned`. Цель: безопасно опубликовать operator transition commands через `/api/v1`, требуя session, CSRF, global permission и capability Channel membership. Assignment не является ACL.

Этапы: contract/RED → минимальная repository/policy+HTTP реализация → OpenAPI → remote CI/dev checks → independent review/QA.

Вне scope: inbox list/read UI, assignment mutation, outbound reply, provider adapters и scheduled wakeup.
