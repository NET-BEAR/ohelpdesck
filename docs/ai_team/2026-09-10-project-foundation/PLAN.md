# Запуск проекта и dev-контура

Статус задачи: in_qa. Дата: 2026-09-10. Владелец: оркестратор.

## Запрос и границы

Подробно проанализировать `/spec`, обновить план работ, начать реализацию; настроить CI/CD для `root@vm742476.vps.masterhost.tech` с ключом `~/.ssh/id_ed25519_masterhost`; подготовить план миграции на GPT-6 Astra; вести документацию. Запрошенный `docs/memory.md` — явное исключение из размещения документации в `docs/ai_team/`.

Исходное состояние: main = 8e5c543, origin = https://github.com/NET-BEAR/ohelpdesck.git. Код отсутствует, четыре ready-спецификации. Пользовательские изменения `.gitignore` и исходные `spec/` сохраняются. Рабочая ветка `codex/project-foundation`.

## Маршрут и документы

Архитектор анализирует спецификации → оркестратор утверждает технический срез в рамках запроса → Разработчик реализует → независимый Ревьювер → QA → отчёт.

Основной process skill: AI Team (подключён пользователем). Capability Routing использован для минимального выбора ролей. OpenAI Docs — только план миграции явно названной модели. Установка plugins/skills не требуется. Memory MCP не разрешён, не используется. GitHub доступ проверен через gh: ADMIN. SSH работает после регистрации ранее неизвестного ключа хоста по TOFU; несовпадающий ключ не отключается. Docker локально доступен, Go/Node в PATH отсутствуют: воспроизводимые проверки через контейнеры.

- RND.md и ARCHITECTURE.md — архитектурный анализ.
- BUSINESS_ANALYSIS.md — требования и последовательность бизнес-срезов.
- ACCEPTANCE_TESTS.md — критерии до реализации.
- GPT6_ASTRA_MIGRATION.md — план, без преждевременного добавления AI в foundation.
- DEV_RUNBOOK.md — эксплуатация и CI/CD.
- REVIEW.md, QA.md — независимые проверки.
- UX_DESIGN.md — продуктовый UI неприменим к первому инфраструктурному срезу; только служебная health-страница по SPEC-000, без проектирования inbox.

## Этапы

1. Анализ всех SPEC-000..030, регистрация разрывов и зависимостей.
2. SPEC-000: 000-01..11 — каркас, API/worker, конфигурация, PostgreSQL, миграции, Redis/S3, HTTP, health, логи и телеметрия.
3. SPEC-000: 000-12..16 — служебный React-каркас, Compose, Makefile, CI и AGENTS.md.
4. SPEC-000: 000-17 — backend/frontend/DB/contract/race проверки; независимые review и QA.
5. Dev: отдельный Compose project, loopback ports, явные миграции, readiness/smoke и rollback; GitHub Actions CI и deploy проверенного ref. Не занимать используемый сервером 443.
6. SPEC-010 после foundation: OIDC, users, RBAC; уточнить issuer/client/bootstrap admin до включения реального входа.
7. SPEC-020 + минимальный transactional outbox из SPEC-030: единый acceptance gate, поскольку core требует durable events уже при создании сообщений.
8. SPEC-030: leasing, fencing, retries, receipts, fault injection и concurrency.
9. Написать и проверить SPEC-040..190 перед их реализацией. Первая целевая вертикаль: attachments → один подтверждённый канал → inbox → routing/templates. Остальные каналы, SLA, workflow, analytics и AI — следующие этапы.

Текущий запуск ограничен завершённым проверяемым инфраструктурным срезом SPEC-000 и dev delivery. Запрос «запускай работу» не трактуется как требование изобрести и реализовать отсутствующие SPEC-040..190.

## Решения и вопросы

- Foundation не требует бизнес-решений пользователя; роли/каналы не придумываются.
- Вопросы следующих этапов с вариантами будут перечислены в RND/BUSINESS_ANALYSIS и не блокируют foundation.
- CI secrets передаются только через GitHub secret storage, не в git или документацию. Содержимое личного SSH-ключа не публикуется; предпочтителен отдельный ограниченный deploy credential.
- Удалённый dev закрыт на loopback и доступен через SSH-туннель до решения о домене и публичном доступе.

Варианты будущих продуктовых решений и последствия перечислены в BUSINESS_ANALYSIS.md, таблица «Решения перед следующими этапами»; полный реестр — GAP-01..20 в RND.md. Они не считаются молча утверждёнными.

## Review и минимальные возвраты

- Infra review: исправлены исполнение архивного shell/YAML от root, потеря retry после broken upload, пересборка current SHA, first-deploy recovery и порты. Повторный независимый review подтвердил устранение; 8 behavioral tests GREEN.
- Runtime review R1–R4: safe OTLP errors, sanitization spans, nested sensitive slog groups, bounded Redis/readiness. Возврат только к backend разработчику, затем тот же независимый reviewer и QA. ACCEPTANCE_TESTS остаются актуальными; первоначальный developer GREEN superseded для затронутых мест до повторных тестов.
- Vulnerability scan инициировал обновление Go 1.25.7→1.25.13 и reachable dependency patches; совпадение advisory не трактуется как подтверждённый exploit.
- Frontend reviewer заметил расхождение readiness validator с OpenAPI; автор добавляет contract parity regression.
- Первый объединённый `make verify` выявил Go-файлы npm dependency `flatted` внутри node_modules: они попали в `go test ./...` и denominator coverage. Tool-container изолирует node_modules пустым volume; обход порога покрытия не используется, production Go по-прежнему полностью включён.
- В workspace появилась другая папка задачи `2026-09-10-chatwoot-inspired-ui`; она вне текущего scope и не включается в commits этой поставки.

## Evidence

Исходный git/remote/worktree проверен. `docker version`: exit 0, Engine 29.7.2. `gh repo view`: ADMIN/main. SSH: Ubuntu 24.04, Docker, свободно 49 GiB, порт 443 занят, `/opt/mtg` существует. Первая SSH-попытка exit 255: хост отсутствовал в known_hosts; повтор accept-new установил соединение. Финальный exit 2 диагностической команды вызван отсутствием `/srv/*`, а не отказом SSH.

## Итоговая объединённая проверка перед dev

`make verify` на 0715a6e + инфраструктурном рабочем дереве: exit 0. Go 88.60% statements / 87.30% executable block lines (385/441), web 27 tests / 90.90% lines, delivery 8 tests, OpenAPI positive+negative, vet/race, govulncheck 0 reachable и npm audit 0. Независимый reviewer approve R1–R5, clean race count=3 exit 0. Процессная QA и GitHub/remote delivery продолжаются.

## Проверка доставки на чистом runner

Git CLI OAuth не имел workflow scope; GitHub connector успешно опубликовал точный workflow (8d3fe49), без расширения scopes. Первый hosted CI run 34441479440 выявил Linux-only defect: root bootstrap container создал .env0600, недоступный runner user. Исправление — запуск bootstrap под UID/GID вызывающего пользователя и проверка readable, без ослабления прав файла.

При обновлении страницы браузера найден nginx301 `/health`→`:8080/health/`: implicit redirect API-prefix конфликтовал с React route. Добавлен exact location /health и smoke200 прямого URL. Минимальный возврат: infra reviewer → QA этих сценариев → повтор hosted CI; backend/UI логика не меняются.
