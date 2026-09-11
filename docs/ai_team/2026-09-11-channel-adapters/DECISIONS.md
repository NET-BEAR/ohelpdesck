# Решения: канальные adapters

Статус: `approved`.

## DEC-CHA-01: Telegram transport / identity

Пользователь подтвердил 2026-09-11 оба режима как разные каналы/адаптеры.

- `telegram_bot`: GoTd Bot API, bot credentials и webhook/update receiver; user-phone/session/2FA поля отсутствуют.
- `telegram_user`: GoTd MTProto user-account, отдельные encrypted persistent session, application `api_id`/`api_hash`, phone authorization и controlled 2FA/relogin lifecycle.

Эти Channel types immutable после create. Они не делят credential schema, worker session lifecycle или UI configuration route. Оба являются двусторонними: inbound, agent reply, delivery/reconciliation status.

## DEC-CHA-02: secret storage

Пользователь подтвердил 2026-09-11 вариант A: application-level AES-256-GCM encrypted blob in PostgreSQL with deployment-injected 32-byte data-encryption key and versioned key ID. Key никогда не хранится в PostgreSQL, git, UI, logs или API response. External KMS/secret manager не добавляется в текущий scope. Plaintext/JSON config запрещены.
