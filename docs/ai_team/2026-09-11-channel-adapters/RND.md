# RND: внешние каналы

Статус: `ready_for_review`. Дата проверки: 2026-09-11.

## Подтверждённые факты

| Канал | Подтверждено источником | Проектное следствие |
|---|---|---|
| Telegram / GoTd | [GoTd](https://gotd.dev/docs/intro/) — Go-клиент MTProto для user и bot, `gotd/botapi` реализует Bot API поверх MTProto; документация отдельно предупреждает о риске abuse/ban при user-account использовании | Не выбирать user-account как техническую деталь; это отдельное product/security решение DEC-CHA-01 |
| Carrot quest | [Developer portal](https://developers.carrotquest.io/) является нормативной точкой API | До фиксации нужных API (incoming webhooks, outbound message или CRM sync) нельзя выдумывать payload/OAuth semantics |
| MTS OmniChannel | PDF пользователя и [MTS Support](https://support.mts.ru/mts_marketolog/rassilki-po-svoei-baze-pro-i-api-k-nim/dokumentatsiya-rest-api): Basic Auth, POST messages, status query, callback, inbound messages; PDF требует callback URL с domain name (не IP), допускает ports 80/443 и даёт `internal_id`/`status` для receipt correlation; document/portal limits различаются по версии | Adapter делает configurable base URL и limit profile; production limit/contract проверяется по данным account manager/sandbox, не по PDF в одиночку |
| MAX | [MAX Bot API](https://dev.max.ru/docs-api) — HTTPS API, актуальный host `platform-api2.max.ru`, access token в `Authorization`, `POST /messages`; webhook HTTPS only | Token/endpoint настраиваются отдельно; inbound subscription и callback signature сверяются по актуальному API перед enable |
| VK | Нужна official API contract именно для Messages/Callback API и выбранного типа сообщества | Не подключать SDK и не хранить access token до выдачи community scopes и callback confirmation flow |

## Что именно говорит приложенный PDF

Документ «1. SMS в HTTP-шлюзе (+MAX)» относится к **MTS OmniChannel SMS HTTP gateway**, а не к MAX messenger bot. В нём заявлены REST, отправка/статусы/callback/inbound, Basic Authentication, `/messages`, `/messages/info`, `/callback`; текст SMS в UTF-8, ориентир 10 сегментов (670 кириллица/1530 латиница). Его нельзя трактовать как контракт Bot API MAX.

## Не подтверждено и не будет предполагаться

- Тип Telegram channel: user account или bot.
- Carrot quest use case, endpoint scope и authentication method.
- OMNI tenant endpoint, sender names, production rate limit и callback allow-list.
- MAX bot token, subscription method/event schema, bot approval status.
- VK group, required scopes, Callback API version/secret, message permissions.

## Security baseline

- Credentials лежат только в encrypted-at-rest store behind application service; API возвращает наличие/health, но никогда secret/ciphertext.
- Webhook route принимает raw body в bounded size, validates TLS-facing route and provider proof before JSON normalization; replay key lives in PostgreSQL through the canonical `(channel_id, external_message_id)` boundary.
- Adapter maps external content to `core.NormalizedInboundMessage`; external structs never cross to core or public DTOs.
- Worker uses finite timeout and error categories: permanent → `MarkFailed`; ambiguous timeout/5xx → no automatic duplicate send, reconciliation first.
