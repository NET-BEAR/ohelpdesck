# Памятка проекта

Обновлено: 2026-09-10. Это локальный документ репозитория, не Memory MCP.

- Репозиторий: `https://github.com/NET-BEAR/ohelpdesck`, основная ветка `main`.
- Исходные требования: `spec/`; подробный план: [PLAN.md](ai_team/2026-09-10-project-foundation/PLAN.md).
- Dev SSH: `ssh -i ~/.ssh/id_ed25519_masterhost root@vm742476.vps.masterhost.tech`.
- Хост: Ubuntu 24.04 LTS, Docker; проект размещается отдельно в `/opt/ohelpdesck-dev`.
- ED25519 fingerprint, впервые принятый по TOFU: `SHA256:IxI2/bxO4aF9xbL+PbGOY2yPD5OLwnQXSLO2MMprvRs`. При несовпадении ключа остановить соединение, не отключать проверку.
- Чужой workload: `mtg-vkru` на 443; не менять и не выполнять host-wide cleanup.
- Плановые внутренние порты: web `127.0.0.1:18080`, API `127.0.0.1:18081`, metrics `127.0.0.1:19090`; PostgreSQL/Redis/MinIO не публикуются на host.
- Доступ к web: `ssh -i ~/.ssh/id_ed25519_masterhost -L 18080:127.0.0.1:18080 root@vm742476.vps.masterhost.tech`, затем `http://localhost:18080`.
- CI/CD: GitHub Actions `.github/workflows/ci.yml`; dev secrets — `DEV_SSH_KEY`, `DEV_SSH_KNOWN_HOSTS` в environment `development`. Личный SSH-ключ не передавать в GitHub.
- Runtime secret file на host: `/opt/ohelpdesck-dev/shared/runtime.env`, права 0600; значения не писать в git, логи или документацию.
- Runbook и фактические результаты запуска: [DEV_RUNBOOK.md](ai_team/2026-09-10-project-foundation/DEV_RUNBOOK.md), [QA.md](ai_team/2026-09-10-project-foundation/QA.md).
- План GPT-6 Astra: [GPT6_ASTRA_MIGRATION.md](ai_team/2026-09-10-project-foundation/GPT6_ASTRA_MIGRATION.md); AI не входит в SPEC-000.

Статус runtime/CI подтверждается только свежим QA evidence, не наличием этой памятки. Проектную документацию продолжать в `docs/ai_team/`; этот файл — запрошенное исключение.
