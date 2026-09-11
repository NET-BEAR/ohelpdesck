# Бизнес-анализ: каналы поддержки

Статус: `ready_for_review`.

## Пользовательская ценность

Администратор подключает независимые точки коммуникации без выдачи секретов операторам. Клиент пишет в выбранный внешний канал; оператор получает provider-neutral Conversation и отвечает из единого рабочего места. Канал сохраняет корректный status отправки, не создаёт дубликат при повторе webhook/job и не сообщает «доставлено», пока provider не подтвердил delivery.

## Роли и права

| Роль | Возможность |
|---|---|
| Administrator с `channel.manage` | Список каналов, create/edit safe config, test connection, enable/disable, назначение channel memberships, health/status history без credentials |
| Agent/Supervisor | Видит и отвечает только в разрешённых `channel_memberships`; не создаёт/не меняет provider config |
| Worker | Забирает durable events/jobs, вызывает provider и фиксирует outcome; не является владельцем Channel configuration |

## Единые правила данных

1. Один Channel — одна конкретная external connection/account/bot, а не «все Telegram/VK».
2. `external_user_id`, `external_chat_id`, `external_thread_id`, `external_message_id` остаются provider-scoped opaque identifiers. Они не являются глобальным ключом Contact.
3. Для inbound обязательны стабильные external message/thread/sender IDs; если provider не даёт id, adapter не включается до согласования safe synthesis/replay strategy.
4. Config хранит только non-secret settings (display name, configured endpoint/options/capabilities). Secret хранится отдельно и доступен adapter only.
5. Enable разрешён лишь после successful validation; `disabled`, `degraded`, `reauthorization_required`, `error` не принимают inbound as active facts.

## Основные сценарии

| ID | Given | When | Then |
|---|---|---|---|
| BA-CHA-01 | Администратор имеет `channel.manage` | Создаёт MAX/VK/Telegram/Carrot/OMNI channel | Видит wizard конкретного типа, вводит safe fields/secret один раз, получает masked summary |
| BA-CHA-02 | Настройка валидна | Нажимает «Включить» | Channel становится active, worker/webhook registration выполняются безопасно; UI показывает activation/result time |
| BA-CHA-03 | Channel active, клиент отправил сообщение | Provider доставляет signed event повторно | Существует ровно один Message/Conversation effect, повтор отмечен duplicate |
| BA-CHA-04 | Агент поставил reply в очередь | Provider accept/status callback приходит | Lifecycle меняется queued→sent/delivered according to proof; UI не подменяет эти состояния |
| BA-CHA-05 | Provider timeout после отправки | Worker не знает принял ли provider request | Сообщение остаётся reconcilable; adapter не создаёт второй external send без provider-specific evidence |
| BA-CHA-06 | Администратор отключил channel | Приходит webhook/job | Нет новых core facts/outbound calls; status и audit event отражают disable |

## Канальные возможности MVP

| Channel type | Первичная capability | Ограничение |
|---|---|---|
| telegram_bot | text inbound/outbound через GoTd Bot API | Отдельный bot token/update receiver; не имеет phone/session/2FA |
| telegram_user | text inbound/outbound через GoTd MTProto | Отдельный encrypted session, phone/2FA/relogin UX и audit-sensitive lifecycle |
| carrotquest | inbound/outbound text only после уточнения API | Не считать CRM API полноценным inbox без подтверждения provider contract |
| omni | outbound SMS + delivery status; inbound только при реально доступном contract | SMS — не conversational rich media channel |
| max | bot text inbound/outbound | Webhook/event contract верифицировать на момент реализации |
| vk | community messages text inbound/outbound | Callback confirmation/token/scope обязательны |
