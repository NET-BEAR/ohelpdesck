# Конфигурация и эксплуатационные контракты

Конфигурация читается один раз при старте. Значения ниже — названия и безопасные defaults, не содержимое runtime secret files.

| Переменная | Назначение / default |
|---|---|
| ENVIRONMENT | development/test/staging/production; default development |
| HTTP_ADDRESS | Listener API, default :8080; в Compose наружу только loopback |
| METRICS_ADDRESS | Отдельный listener, default :9090; запрещено публиковать в Internet |
| DATABASE_URL | Обязательно; pgx connection URL, никогда не логировать |
| DATABASE_MAX_CONNECTIONS | Положительное число, default 10; сумма API+worker входит в DB budget |
| REDIS_URL | Обязательно для конфигурации клиента; optional runtime readiness foundation |
| S3_ENDPOINT | S3-compatible host:port |
| S3_BUCKET | Имя подготовленного bucket |
| S3_ACCESS_KEY / S3_SECRET_KEY | Обязательные credentials; без defaults |
| S3_USE_SSL | bool; dev false для внутреннего MinIO, production TLS выбирается явно |
| HTTP_READ_TIMEOUT | Положительная duration, default 10s |
| HTTP_WRITE_TIMEOUT | Положительная duration, default 15s |
| HTTP_IDLE_TIMEOUT | Положительная duration, default 60s |
| SHUTDOWN_TIMEOUT | Положительная duration, default 10s; меньше termination grace 30s |
| OTEL_EXPORTER_OTLP_ENDPOINT | Необязательный collector endpoint |
| CORS_ALLOWED_ORIGINS | Через запятую; пусто = deny для cross-origin |

Дополнительные переменные Compose: POSTGRES_PASSWORD, S3_ACCESS_KEY, S3_SECRET_KEY генерируются bootstrap; RELEASE_SHA задаёт tag приложения; SOURCE_ROOT используется trusted remote manifest для source build context. Не передавать runtime environment в issue/PR.

Health: `/health/live` без сети; `/health/ready` проверяет PostgreSQL и optional Redis/S3 в ограниченном бюджете. PG unavailable =503, optional failure=degraded при ready200. Не использовать liveness для проверки доступности БД. Full error JSON определён в `api/openapi.yaml`; user-facing startup диагностика выводит только безопасные ошибки конфигурации.

Минимальные метрики: `http_requests_total`, `http_request_duration_seconds`, `process_start_time_seconds`, `db_pool_acquired_connections`, `db_pool_idle_connections`. Business metrics и alerts появятся с соответствующими spec; foundation не имитирует обработку jobs.

Migration CLI: `/app/migrate up|down|status`. SQL встроен в binary; foundation выполняет ordered bookkeeping migration под advisory lock в явной транзакции. При добавлении следующей версии требуется расширить runner и проверить forward path; текущий единственный foundation step нельзя выдавать за готовый runtime всех будущих миграций.
