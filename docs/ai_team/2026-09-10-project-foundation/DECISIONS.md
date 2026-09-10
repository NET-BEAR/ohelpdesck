# Решения

2026-09-10. Основание: прямой запрос пользователя, ready SPEC-000 и архитектурный анализ.

1. Первый реализуемый срез — SPEC-000 + удалённый dev delivery. SPEC-010..030 остаются в последовательном плане; отсутствующие 040..190 сначала описываются.
2. Сохранить Go/React modular monolith и PostgreSQL durability. Redis/S3 degraded по foundation policy, PG mandatory; metrics loopback отдельно.
3. ADR-ARCH-003 принят как порядок работ: общий durability gate для core/outbox. Бизнес-решения GAP-02/03/05/06/07 не утверждены этим решением.
4. Пользовательские `.gitignore` и `spec/` сохраняются. Работа ведётся в codex branches, implementation worktrees раздельны.
5. Для подключения использовать указанные root/host/key. SSH host key зарегистрирован впервые (TOFU); далее pin, StrictHostKeyChecking=yes в CI.
6. Dev изолирован в `/opt/ohelpdesck-dev`, loopback 18080/18081/19090. Контейнер `mtg-vkru`/443 не изменять. Никаких общих docker prune и volume deletion.
7. GitHub CI хранит отдельный deploy credential с forced command и restrict. Личный ключ `id_ed25519_masterhost` остаётся локальным. Dev runtime credentials генерируются на host, не коммитятся.
8. CI/CD разрешено запросом пользователя. Для запуска и review создаётся ветка и draft PR; main не сливается автоматически. На bootstrap-ветке CI/CD включён явно; после merge удалить bootstrap branch trigger.
9. GPT-6 Astra — только план до SPEC-160, без смены модели на другую и без платных API-запросов.
10. `docs/memory.md` создаётся по явно названному пользователем пути как project runbook pointer, не Memory MCP. Остальная подробная документация в `docs/ai_team/`.
