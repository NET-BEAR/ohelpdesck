# UX: администрирование каналов

Статус: `ready_for_review`.

## IA

В существующем разделе «Настройки → Каналы» administrator видит список подключений с колонками: name, type, enabled/status, last success, last error category и назначенные операторы. Credentials, external IDs и provider response отсутствуют. Для каждого из пяти типов есть самостоятельный route/wizard, а не один общий JSON editor.

```text
Настройки / Каналы
├── Подключения (список)
├── Telegram Bot (GoTd)
├── Telegram User account (GoTd MTProto)
├── Carrot quest
├── MTS OmniChannel SMS
├── MAX Bot
└── VK Messages
```

## Общий wizard

1. **Основное:** понятное имя, тип (immutable after creation), non-secret endpoint/options, список capability.
2. **Авторизация:** secret/token/password вводится по защищённому полю лишь при create/rotate; повторно не отображается. UI говорит «сохранён», а не пытается показать значение.
3. **Проверка:** явная кнопка «Проверить подключение» с pending/success/safe failure state, request ID без provider payload.
4. **Активация:** отдельная confirm action с понятным external effect (register webhook/subscription); switch не является неявным подтверждением рискованной мутации.
5. **Операторы:** server-backed membership capability `read/reply/reassign`; UI не заменяет server permission checks.

## Поля по каналам

| Канал | Поля до конкретного provider contract | Не показывать |
|---|---|---|
| Telegram Bot | display name, bot identifier, webhook/update mode | Bot token |
| Telegram User account | display name, application identifier, connection/session status; explicit re-login/2FA flow | `api_hash`, phone number, session/2FA values |
| Carrot quest | display name, selected supported use case/API version, site/project identifier | API token, raw visitor payload |
| OMNI | display name, base URL profile, sender name, delivery callback URL; Basic login/password | Basic password, full recipient/message examples |
| MAX | display name, bot ID, webhook/subscription endpoint | access token |
| VK | display name, group ID, Callback API version/endpoint | community access token, callback secret |

## Accessibility and failure UX

- Wizard has one `h1`, labelled fields, inline validation, keyboard-operable navigation, visible focus and non-color status labels.
- `disabled`, `active`, `degraded`, `reauthorization_required`, `error` are text labels plus icon, never color alone.
- Leaving with unsaved non-secret edits requires confirmation; secrets are never kept in browser local storage, URL, autosave or error copy.
- Validation/retry is explicit; failed test never enables the channel automatically.
