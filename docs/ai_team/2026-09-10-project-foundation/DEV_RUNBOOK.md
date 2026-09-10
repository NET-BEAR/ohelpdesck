# Разработка и доставка в удалённый dev

Документ описывает настроенный механизм. Итоговый commit/run и результаты runtime-проверок фиксируются в QA.md.

## Локальный запуск

Требуется Docker Desktop либо Docker Engine + Compose и GNU Make. Устанавливать Go, Node, PostgreSQL, Redis и MinIO на host не нужно. Версии build/test tools закреплены в Dockerfile/Compose/Makefile, npm dependencies в lockfile, Go modules в go.mod/go.sum.

```sh
make bootstrap
make dev
make verify
bash deploy/smoke.sh
make stop
```

`bootstrap` создаёт `.env` с случайными local credentials и правами 0600, только если файл отсутствует. Не печатает значения и не заменяет существующую конфигурацию. `stop` сохраняет volumes. `make test-integration` использует отдельную foundation БД dev; тесты миграций нельзя запускать против базы реальных пользователей.

Web: `http://localhost:18080`, API: `http://localhost:18081/health/live`, readiness `/health/ready`, metrics `http://localhost:19090/metrics`. Порты фиксированы и bound к loopback. В foundation web показывает служебное состояние и не является inbox.

`make migrate-up`, `make migrate-status`, `make migrate-down` — явные команды. Application startup схемы не меняет. Down выполнять только для подтверждённо безопасной миграции; deploy не запускает down автоматически.

## Удалённый dev

```sh
ssh -i ~/.ssh/id_ed25519_masterhost root@vm742476.vps.masterhost.tech
ssh -i ~/.ssh/id_ed25519_masterhost -L 18080:127.0.0.1:18080 root@vm742476.vps.masterhost.tech
```

В браузере после туннеля открыть `http://localhost:18080`. При занятом локальном порте можно использовать `-L 28080:127.0.0.1:18080` и открыть localhost:28080. Публичный domain/TLS не настраивался: порт 443 занят существующим mtg.

Проверено: Ubuntu 24.04 LTS, Docker 28.2.2. Compose v2.39.4 установлен отдельно; бинарник проверен SHA-256 `7af95166a730b87e172d4fc9aefea8725d3c6c7327d59149267b452114ddb7d4` по metadata официального Docker release. Для сборки установлен Docker Buildx v0.26.1, SHA-256 `9451034b6ca5354e8bf88a2002a413aedabf110fd0f12ebb0b2f2cc241be8e41`. Engine и existing containers не обновлялись.

## Структура host

```text
/opt/ohelpdesck-dev/
  bin/receive-deploy         # operator-installed trusted script
  bin/remote-apply           # operator-installed trusted script
  config/compose.yml         # operator-installed trusted manifest
  shared/runtime.env         # 0600; random runtime credentials
  releases/<git SHA>/        # validated source archives
  release-checksums/<SHA>    # immutable source checksum for retry
  current                   # symlink to last healthy release
  previous                  # symlink to prior healthy release
  current-sha
  deploy.lock
```

Compose project: `ohelpdesck-dev`. Named volumes содержат только данные этого проекта. PostgreSQL, Redis и MinIO не публикуются на host. Metrics доступен только loopback. App user принудительно 65532:65532, web 101:101; app root filesystem read-only, capabilities сброшены.

Trusted scripts/manifest **не обновляются архивом CI**. Их обновляет оператор через личный SSH credential после review. `SOURCE_ROOT` в trusted manifest задаёт только context сборки конкретного релиза. Изменяя инфраструктуру, сначала проверить совместимость manifest с предыдущим релизом, затем установить новые trusted файлы. Приложение, имеющее доступ к БД и объектам проекта, остаётся доверенным обработчиком этих данных; Docker builder/engine и supply chain являются границей доверия.

## GitHub Actions

Workflow: `.github/workflows/ci.yml`.

1. Push main/codex branches и pull request: locked bootstrap, Go tests/coverage/vet/race, real PostgreSQL/Redis/MinIO, frontend typecheck/lint/unit/build, OpenAPI positive/negative validation, delivery behavioral tests, container build, runtime smoke.
2. Deploy зависит от успешного verify и работает только для main и bootstrap-ветки `codex/project-foundation`. После merge bootstrap condition удаляется отдельным небольшим изменением.
3. Environment `development` разрешает только эти две ветки. Pull requests не получают dev secrets и не деплоятся.
4. Secrets `DEV_SSH_KEY` и `DEV_SSH_KNOWN_HOSTS` сохранены в environment. Key отдельный, forced command `receive-deploy` + `restrict`; личный ключ в GitHub не загружался.
5. SSH использует `-F /dev/null`, `IdentitiesOnly=yes`, `StrictHostKeyChecking=yes`: ни fallback на локальный ключ, ни автоматический trust неизвестного хоста.
6. Архив `git archive HEAD` соответствует tested SHA. Receiver ограничивает размер, запрещает path traversal/links/special files и проверяет совпадение checksum при повторе SHA. Staging очищается при ошибке.
7. Trusted host deploy строит app images, поднимает dependencies, явно применяет миграции, запускает apps, проверяет ready/live/web и только после success обновляет current.

Первый запуск не требует workflow на main: push bootstrap-ветки запускает CI и dev. `workflow_dispatch` становится доступен штатно после попадания workflow на default branch. Workflow source review в draft PR; main автоматически не сливается.

## Rollback и повтор

- До переключения app ошибка сборки/миграции останавливает rollout; старые app images остаются запущенными. Миграции должны быть backward compatible, до contract migration отдельная проверка.
- После неуспешного app switch trusted script восстанавливает прошлый image SHA. БД не откатывается и volumes не удаляются.
- Если это первый релиз, останавливаются только новые api/worker/web; dependencies/data сохраняются.
- Повтор current SHA не пересобирает images и только восстанавливает/проверяет этот runtime. Другой архив под тем же SHA отвергается.
- Failed rollback требует оператора, success не записывается. Диагностика: `docker compose ... ps`, bounded logs с redaction, `current-sha`, health endpoints. Не печатать `docker inspect` целиком или `compose config`, потому что там могут быть credentials.
- Rollback к `previous` вручную: оператор запускает trusted `remote-apply` с предыдущим SHA под `flock` того же deploy.lock. Проверить schema compatibility заранее.

Нельзя запускать `docker system prune`, `down -v` на dev host или изменять mtg/443 для этой задачи. CI `down -v` применяется только к эфемерному runner.

## Проверки источников

Механизм установки соответствует [Docker Compose plugin installation](https://docs.docker.com/compose/install/linux/). Разделение triggers/environment соответствует [GitHub deployment controls](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments). Credentials передаются через [GitHub Actions secrets](https://docs.github.com/en/actions/concepts/security/secrets).

## Фактическая приёмка 2026-09-10

[CI run 34441730021](https://github.com/NET-BEAR/ohelpdesck/actions/runs/34441730021): verify/deploy-dev success, SHA `62349369d72263d08be2a518d88c4ec97d52d21c`. API live/ready и web `/health` возвращают 200. Отдельная fault injection с API `CMD /bin/false` завершилась ожидаемым exit 1 и автоматическим восстановлением прежних app images, без отката БД. Независимая QA подтвердила current-sha, health, UID и ports после восстановления. Повтор исправного SHA через forced-command key: exit 0, already current and healthy, без rebuild/migrate. Неверный host key: exit 255; запрещённая команда: exit 64. Полное evidence — QA.md.

Первый CI выявил ownership `.env` на Linux runner, браузерный reload — redirect `/health`; оба дефекта исправлены, прошли отдельный review, QA и успешный повтор CI. Существующий mtg сохранил uptime около пяти месяцев; его ранее существовавший статус unhealthy не относится к этой поставке.
