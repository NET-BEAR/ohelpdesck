# Памятка проекта

Обновлено: 2026-09-10. Это локальный документ репозитория, не Memory MCP.

- Репозиторий: `https://github.com/NET-BEAR/ohelpdesck`, основная ветка `main`.
- Исходные требования: `spec/`; подробный план: [PLAN.md](ai_team/2026-09-10-project-foundation/PLAN.md).
- Dev SSH: `ssh -i ~/.ssh/id_ed25519_masterhost root@vm742476.vps.masterhost.tech`.
- Хост: Ubuntu 24.04 LTS, Docker; проект размещён отдельно в `/opt/ohelpdesck-dev`.
- ED25519 fingerprint, впервые принятый по TOFU: `SHA256:IxI2/bxO4aF9xbL+PbGOY2yPD5OLwnQXSLO2MMprvRs`. При несовпадении ключа остановить соединение, не отключать проверку.
- Чужой workload: `mtg-vkru` на 443; не менять и не выполнять host-wide cleanup.
- Фактические внутренние порты: web `127.0.0.1:18080`, API `127.0.0.1:18081`, metrics `127.0.0.1:19090`; PostgreSQL/Redis/MinIO не публикуются на host.
- Доступ к web: `ssh -i ~/.ssh/id_ed25519_masterhost -L 18080:127.0.0.1:18080 root@vm742476.vps.masterhost.tech`, затем `http://localhost:18080`.
- CI/CD: GitHub Actions `.github/workflows/ci.yml`; dev secrets — `DEV_SSH_KEY`, `DEV_SSH_KNOWN_HOSTS` в environment `development`. Личный SSH-ключ не передавать в GitHub.
- Runtime secret file на host: `/opt/ohelpdesck-dev/shared/runtime.env`, права 0600; значения не писать в git, логи или документацию.
- Runbook и фактические результаты запуска: [DEV_RUNBOOK.md](ai_team/2026-09-10-project-foundation/DEV_RUNBOOK.md), [QA.md](ai_team/2026-09-10-project-foundation/QA.md).
- План GPT-6 Astra: [GPT6_ASTRA_MIGRATION.md](ai_team/2026-09-10-project-foundation/GPT6_ASTRA_MIGRATION.md); AI не входит в SPEC-000.

Статус runtime/CI подтверждается только свежим QA evidence, не наличием этой памятки. Проектную документацию продолжать в `docs/ai_team/`; этот файл — запрошенное исключение.

Проверено 2026-09-10: [CI/CD run 34441730021](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34441730021) успешен; dev runtime `62349369d72263d08be2a518d88c4ec97d52d21c`. Реальные rollback и повтор SHA прошли, health 200. Поставка на review: [draft PR #1](https://github.com/NET-BEAR/ohelpdesck/pull/1). Эти сведения — снимок проверки, последующие релизы сверять по `current-sha` и QA.
