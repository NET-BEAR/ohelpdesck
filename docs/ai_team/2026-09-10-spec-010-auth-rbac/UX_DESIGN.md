# UX boundary for SPEC-010

Статус: `ready_for_review`.

Реализуемые login/admin screens follow the approved target design in `../2026-09-10-chatwoot-inspired-ui/UX_DESIGN.md`. This task adds contracts, not a competing visual design.

For this runtime slice, the public screen is a local login form with login/password fields, generic failure and no credential persistence outside the submitted request. The former invitation/OIDC continuation is superseded for authentication only and returns in the Keycloak adapter task. Admin screens present user status, system role, bundle assignments and effective-access preview received from the server; they do not compute authorisation locally.
