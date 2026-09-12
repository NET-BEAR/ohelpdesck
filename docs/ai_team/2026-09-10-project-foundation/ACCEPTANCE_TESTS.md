# Критерии приёмки до реализации

Статус: approved для реализации SPEC-000 на основании ready-спецификации и запроса пользователя. Это не отметка о прохождении проверок.

| ID / spec | Given | When | Then |
|---|---|---|---|
| F01 / AC-FND-001,007 | Чистая копия, Docker Compose | bootstrap, dev, test | Зависимости и API/worker/web стартуют, команды воспроизводимы |
| F02 / AC-FND-002,003 | Валидная конфигурация, PostgreSQL доступен | live/ready | 200, ready с именованными checks |
| F03 / AC-FND-004 | Рабочий процесс | PostgreSQL отключается | live=200, ready=503; восстановление возвращает ready=200 |
| F04 / AC-FND-005 | API и worker запущены | SIGTERM | Завершение в пределах timeout без зависания |
| F05 / AC-FND-006,012 | Пустая PostgreSQL | migrate-up/status/down/up | Только bookkeeping, детерминированное применение и повторный запуск |
| F06 / AC-FND-008 | Валидный/невалидный OpenAPI | validator | Валидный проходит, невалидный отвергается |
| F07 / AC-FND-009 | Нет генерации | make generate | Явно документированный no-op; при введении генерации включается drift check |
| F08 / AC-FND-010 | Тестовые секреты включая вложенные поля | Структурированное логирование и ошибки | Значения отсутствуют в capture, ошибки не раскрывают raw DSN |
| F09 / AC-FND-011 | Frontend lockfile | typecheck/lint/unit/build | Проходят; страница использует общий API client и query/router/error boundary |
| F10 / SEC-FND-001..010 | Ошибочная конфигурация, чужой Origin, большой body | Валидация/HTTP | Fail closed; ограничение тела; non-root контейнеры; timeouts; CORS deny default |
| F11 / Mandatory tests | PostgreSQL, Redis, MinIO | Integration suite | connect/tx rollback, Redis failure, S3 put/get/delete/presign/health проверены |
| F12 / Telemetry | HTTP/DB/outbound calls, недоступный exporter | Runtime | Метрики/trace hooks существуют, сбой exporter не падает на messaging |
| D01 | CI pull request без deploy secrets | Pipeline | Backend test/vet/race, frontend checks, migrations/integration/OpenAPI выполняются; SSH не используется |
| D02 | Проверенный ref и dev credentials | Deploy | Выкладывается конкретная версия, миграции явно, readiness проверяется, secrets не попадают в логи |
| D03 | Незнакомый/изменённый host key | CI SSH | Соединение отвергается pinned known_hosts |
| D04 | Неудачная новая версия | Deployment | Предыдущая версия приложения восстанавливается; автоматический destructive rollback DB запрещён |
| D05 | Сервер с другими workload | Deployment | Только отдельный проект ohelpdesck-dev, existing 443/mtg не изменяются |

До GREEN сохранить RED evidence соответствующих тестов. Для нового Go-кода целевое покрытие изменённых строк >=80%, критические security branches отдельно. При отсутствии доказательства критерий остаётся unverified и SPEC-000 не становится done.
