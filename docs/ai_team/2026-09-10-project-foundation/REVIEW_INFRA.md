# Независимый review инфраструктуры

Дата: 2026-09-10. Статус: ready_for_review; исправленные замечания перепроверены, текущих подтверждённых P0/P1 в рассмотренном инфраструктурном срезе не осталось. Это не QA/приёмка deployed runtime.

Роль: Ревьювер. Инфраструктурный код написал оркестратор; автор этого отчёта его не изменял. Scope: `deploy/**`, `Makefile`, `.github/workflows/ci.yml`, PLAN/DECISIONS/ACCEPTANCE_TESTS, GPT6_ASTRA_MIGRATION. Незавершённый backend/web не анализировался. Единственный изменённый ревьювером файл — этот отчёт. `.env`, private keys и runtime secrets не читались, удалённые команды не выполнялись.

## Замечания и результат повторной проверки

| ID | Приоритет | Первоначальный дефект | Исправление и evidence |
|---|---|---|---|
| INFRA-01 | P1, исправлено | Forced receiver исполнял `bash` из загруженного архива от root; затем использовал архивный Compose. Ограничение SSH command не ограничивало host execution | Receiver вызывает только operator-installed `bin/remote-apply`; тот использует только operator-installed `config/compose.yml`. Archive — build context. Принудительные runtime UID/capabilities/read-only в trusted manifest. Sandbox test архивного script `exit 99`: deployment вызвал trusted stub и завершился 0, архивный script не исполнялся |
| INFRA-02 | P2, исправлено | Создание final release directory до upload/validation делало SHA неретрабельным после transient ошибки | Temporary staging, cleanup trap, content checksum. Sandbox: invalid archive exit1 без final dir; valid archive exit0; identical retry exit0; different archive того же SHA exit65; staging после тестов отсутствует |
| INFRA-03 | P1, исправлено | Повтор current SHA пересобирал/перетегировал единственный image и при failure попадал в first-deploy cleanup вместо rollback | Current-SHA fast path использует `up --no-build`, health и return. Sandbox: current retry exit0, build=false, stop=false |
| INFRA-04 | P2, исправлено | Первый неуспешный deployment оставлял новые нездоровые app services | First failure останавливает только api/worker/web. Sandbox startup fault exit43; stop=true, volumes не удаляются. Upgrade fault exit43 вызывает старый RELEASE_SHA без build |
| INFRA-05 | P2, исправлено | Compose предлагал PORT overrides, smoke/deploy проверяли фиксированные 18080/18081 | Overrides удалены; declared ports и smoke согласованы; loopback 18080/18081/19090 |

При повторном review не обнаружено нового прямого выполнения загруженного shell/YAML на host. Это **не абсолютная песочница**: Docker daemon/BuildKit, образы и Dockerfile supply chain остаются доверенной вычислительной базой. CI credential разрешает разворачивать приложение, а приложение получает dev runtime credentials и доступ к собственным данным/сети; SHA checksum защищает повторяемость одного upload, но не доказывает GitHub происхождение SHA. Подтверждение protected branch/environment settings и root ownership host-side файлов требует отдельного runtime evidence оркестратора.

## Оставшиеся ограничения и follow-up

| ID | Уровень | Наблюдение | Следующий шаг |
|---|---|---|---|
| INFRA-F01 | P2, до production | CI отправляет source archive, host пересобирает; базовый Debian tag и apt indexes не закреплены digest/snapshot. Проверенный CI binary не тождествен remote image | Документировать, что сейчас доставляется проверенный source commit. Для production build once, сохранить image digest/provenance и продвигать этот image без пересборки |
| INFRA-F02 | P2, до schema evolution | Rollback trap активируется после dependencies/migrate; trusted manifest один для старого и нового релиза. Это приемлемо для стабильного foundation manifest, но не обеспечивает rollback изменения PG/version/config/schema | Перед обновлением manifest/version dependency — отдельный operator rollout и проверка backward compatibility; forward-only schema recovery. Не обещать D04 для destructive migrations |
| INFRA-F03 | P2, QA gate | Makefile сохраняет Go coverage, но не сравнивает с 80% и не проверяет падение общего покрытия | QA обязан проверить порог свежим evidence; для последующих PR добавить механический coverage gate, иначе `make verify` само по себе не доказательство выполнения требования |
| INFRA-F04 | P2, перед завершением | PLAN ссылается на DEV_RUNBOOK/QA, которые во время review ещё готовятся; эксплуатационные сценарии не подтверждены наличием scripts | В runbook описать operator-only update trusted manifest, first-deploy failure, same-SHA retry, old-image rollback, сохранение volumes и доказательства проверки на host |
| INFRA-F05 | P3, до расширения | `generate` — явный no-op, а drift-check ограничен `api/openapi.yaml` | При появлении генерации расширить check на все generated paths и untracked output; для текущего foundation AC-FND-009 условно неприменим |

CI PR не имеет SSH deploy job; deploy требует verify и конкретный branch/ref. Checkout action закреплён commit. SSH использует BatchMode/StrictHostKeyChecking/pinned known_hosts, отдельный ключ, `-F /dev/null` и `IdentitiesOnly=yes`; это предотвращает fallback к личному ключу через config. Наличие environment `development` в YAML не доказывает его protection settings — root сообщает об их настройке отдельно.

## Compose и Makefile

Проверено: PG/Redis/MinIO не публикуют host ports; app/web/metrics опубликованы только на loopback; Dockerfile application USER дополнен trusted Compose user; app read-only с tmpfs, no-new-privileges и cap_drop. Web имеет отдельный UID. Не обнаружены runtime docker.sock/host-root binds/privileged для api/worker/web. Tool service `go` монтирует source для локальных проверок; remote deploy его не запускает. Bootstrap создаёт случайные credentials с O_EXCL и 0600, не перезаписывает существующее и не выводит значения.

`make verify` включает backend tests/vet/race/fmt, frontend checks, OpenAPI validation и negative control. Реальная полнота интеграционных тестов, процессных shutdown тестов и observability требует backend review/QA, который не входил в scope. Compose config проходил с synthetic placeholders и `--env-file /dev/null`, без чтения `.env`. CI teardown `down -v` ограничен ephemeral hosted runner; remote rollback volumes не удаляет.

## План exact GPT-6 Astra

`GPT6_ASTRA_MIGRATION.md` достаточен как подробный **план будущей миграции/интеграции**, соответствующий текущему проекту без AI-кода. Сохранены точное имя GPT-6 Astra и `gpt-6-astra`, официальная source citation, отличия Responses/parameters и ограничения проверки доступа. Модель не заменена другой. Документ не объявляет запросы/доступ/квоты проверенными.

Есть восемь последовательных шагов: SPEC-160/context permissions → access smoke → contracts → adapter → synthetic prompt/evals → baseline → shadow → staged enable/rollback. Определены кандидатные пути, failure/refusal/incomplete outcomes, prompt injection/RBAC проверки и отключение AI без влияния на messaging. Отсутствие численных latency/cost/quality thresholds обозначено честно; они должны быть утверждены в SPEC-160 до launch. Для начала foundation это не blocker. API rollout, SDK version и фактическая совместимость должны проверяться заново при реализации; в данном review официальные факты независимо не запрашивались, оценивалась полнота уже source-cited плана.

## Evidence выполненных проверок

- `bash -n deploy/remote-apply.sh deploy/receive-deploy.sh deploy/smoke.sh` — exit0.
- Python `ast.parse` для `bootstrap.py`, `validate_openapi.py` — exit0.
- `docker compose --env-file /dev/null -f deploy/compose.yml config --quiet` с synthetic values — exit0.
- Receiver sandbox на bundled Python: invalid upload=1, first valid=0, identical retry=0, changed content=65; trusted invocation counts 0/1/2/2; temporary staging remaining=0. Любая ошибка ожидания приводила бы к assertion failure; общий exit0. Использованы временные копии scripts с заменой только deploy root и stubs flock/stat/checksum для host-independent теста.
- Apply sandbox с fake Docker/curl и Linux-compatible readlink stub: same SHA exit0 без build/stop; first failure exit43 со stop app only; upgrade failure exit43 с вызовом old SHA. Общий exit0. Это проверка control flow, не реального Docker health/rollback.
- Системный Python 3.9 не поддерживает `tarfile.extractall(filter='data')`; первоначальная попытка valid-archive sandbox не могла доказать этот путь. Проверка повторена успешно на bundled Python, поддерживающем filter. Удалять безопасный filter ради локального старого Python не требуется; host prerequisite — Python с поддержкой data filter.

Все временные файлы sandbox удалены. Реальный SSH/permissions, Docker build, migration smoke, host restart, D04 rollback и полный CI run должен подтвердить QA отдельно. Прохождение этих review checks не означает `SPEC-000 done`.

## Передача

status: ready_for_review, исправления инфраструктурных замечаний подтверждены в рамках указанного scope.  
artifacts: REVIEW_INFRA.md.  
evidence: статический анализ, syntax/config проверки, изолированные receiver/apply failure-injection проверки, completeness review migration plan.  
risks: F01–F05 и отсутствие самостоятельной runtime/CI проверки.  
recommended_next_role: независимый Ревьювер для завершённых backend/web, затем QA полного foundation/dev delivery.

## Снимок файлов последней проверки

SHA-256 локального содержимого, не Git commit:

- `deploy/receive-deploy.sh`: `74700ab63a40fe6bd2915f2e1353ff4c62843441c90ec962f53ce59b4bb144f0`
- `deploy/remote-apply.sh`: `5e5003bd3b9fae0dc6ed0b3f6aee00ec65d1062f3822d26e67306af3a08c908c`
- `deploy/compose.yml`: `2daa9c61aa6202aac17c225215473da219c71eed35a728793a6fba8b4b0e6ff6`
- `Makefile`: `2a6b3c7340ea99683765058630b19e4104ce5113beafaf410491af282865f5c3`
- `.github/workflows/ci.yml`: `b304fca95c53f85bfd1b6e608d55ca989f020578bf857e6e553692a207741a0e`

## Повторный review после clean CI и browser QA

Статус: **approved по исходному коду для указанного reroute**, runtime GREEN должен подтвердить QA. Основание повторной проверки: оркестратор сообщил о реальном CI run `34441479440` с permission denied при чтении root-owned `.env` 0600 и browser-ошибке redirect `/health` на внутренний порт 8080. Эти внешние evidence переданы оркестратором; ревьювер самостоятельно CI/browser здесь не запускал.

Scope: изменения `Makefile`, `deploy/nginx.conf`, `deploy/smoke.sh`, а также изоляция npm dependencies в Go tooling service `deploy/compose.yml`. Backend suites и production-файлы ревьювер не запускал/не изменял.

| Изменение | Вывод независимого review | Требуемое QA |
|---|---|---|
| Bootstrap `--user "$$(id -u):$$(id -g)"` и `test -r "$(ENV_FILE)"` | Исправляет первопричину на Linux runner: создатель 0600 файла теперь тот же UID, который запускает последующие Compose commands. Читаемость проверяется сразу. Secret не печатается, permissive chmod не вводится | Clean checkout на GitHub-hosted runner: bootstrap + следующий Compose шаг проходят; file owner соответствует runner, mode 0600. Старый чужой root-owned файл намеренно не исправляется молча |
| Nginx `location = /health { try_files /index.html =404; }` | Exact location предшествует prefix `/health/`; SPA route получает index, infrastructure `/health/live` и `/health/ready` сохраняют proxy path. Не требуется redirect на port 8080 | Rebuilt nginx: direct GET `/health` = 200 без Location, `/health/ready` остаётся JSON backend; browser reload route работает |
| Smoke direct `/health` status assertion | Проверяет именно 200 без `-L`, поэтому неожиданный 301 не превращается в ложный GREEN; bounded curl | Smoke на rebuilt stack и отдельная browser QA |
| Go service anonymous `/src/web/node_modules` volume | Скрывает frontend dependency tree от `go test ./...`/`-coverpkg=./...` внутри tooling container. Не удаляет host npm files и не затрагивает web build. Empty directory из Go image перекрывает вложенный путь bind-mounted checkout | Повторить Go coverage/race при установленном frontend node_modules; npm packages с Go fixtures не входят в список покрываемых application packages |

Новых P0/P1/P2 дефектов в этом узком diff не обнаружено. Изоляция node_modules не является исключением production Go packages из coverage — она исключает сторонний frontend dependency tree.

Проверки reroute: `bash -n deploy/smoke.sh` — exit0; `docker compose --env-file /dev/null -f deploy/compose.yml config --quiet` с synthetic placeholders — exit0; `make -n bootstrap ENV_FILE=/tmp/review-placeholder-config` — exit0, shell UID/GID substitutions и проверка readable сохранены в recipe. Dry-run не создавал конфигурацию и не читал `.env`.

Уточнение ранее записанного INFRA-F03: текущий Makefile уже вызывает `deploy/check_coverage.py`, который отвергает пустой отчёт и минимум statement/executable-block line coverage ниже 80%. Старое утверждение «Makefile не проверяет порог» superseded; полная корректность метрики/покрытие изменённых строк остаётся предметом общего backend review/QA, вне этого reroute.

recommended_next_role: QA — подтвердить clean CI permissions, nginx direct route/browser reload и coverage isolation на реальных запусках. Approval этого раздела не заменяет runtime evidence и не меняет общий task status.

Снимок повторно проверенных файлов (SHA-256):

- `Makefile`: `91f27be760c3759af9f90bd08f37457f02a689dd2aea73f05b79bbfb237b3b1a`
- `deploy/nginx.conf`: `ba5049ce0dda696515e639ea0626bab2f14e0711803080cc24a266f3b8ea8750`
- `deploy/smoke.sh`: `8d354d093a157431229001278915acb8dfb592fd3cbdf19e666be93cb311d982`
- `deploy/compose.yml`: `c2188948056fda0ca7a18a2a779596ce7e25c05b65ecc79037cb991aa1261ab1`
