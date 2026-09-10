#!/usr/bin/env bash
set -euo pipefail
compose=(docker compose --env-file "${ENV_FILE:-.env}" -f deploy/compose.yml)
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18081/health/live
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18080/health/ready
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18080/ >/dev/null
test "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/metrics)" = 404
trap '"${compose[@]}" start postgres >/dev/null' EXIT
"${compose[@]}" stop postgres
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18081/health/live
test "$(curl --max-time 10 -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18081/health/ready)" = 503
"${compose[@]}" start postgres
for attempt in $(seq 1 30); do
  if curl --fail --silent --max-time 5 http://127.0.0.1:18081/health/ready; then
    echo 'Runtime smoke passed, including PostgreSQL loss and recovery'
    exit 0
  fi
  sleep 1
done
exit 1
