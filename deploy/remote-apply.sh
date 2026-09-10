#!/usr/bin/env bash
set -euo pipefail
release_sha=${1:?commit SHA required}
[[ "$release_sha" =~ ^[a-f0-9]{40}$ ]] || exit 64
deploy_root=/opt/ohelpdesck-dev
release_path="$deploy_root/releases/$release_sha"
export RELEASE_SHA="$release_sha"
export SOURCE_ROOT="$release_path"
# This manifest is installed with the operator credential, never from CI uploads.
compose=(docker compose --project-name ohelpdesck-dev --env-file "$deploy_root/shared/runtime.env" -f "$deploy_root/config/compose.yml")
previous_path=$(readlink -f "$deploy_root/current" 2>/dev/null || true)
if [[ "$previous_path" == "$release_path" ]]; then
  # A retry of an already accepted release must not rebuild or retag its images.
  "${compose[@]}" up -d --no-build --wait --wait-timeout 150 api worker web
  curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18080/health/ready
  echo "Deployment already current and healthy: $release_sha"
  exit 0
fi

rollback() {
  result=$?
  trap - ERR
  if [[ -n "$previous_path" && -d "$previous_path" && "$previous_path" != "$release_path" ]]; then
    echo 'Deployment failed; restoring previous application images (database is not rolled back)' >&2
    export RELEASE_SHA="$(basename "$previous_path")"
    export SOURCE_ROOT="$previous_path"
    "${compose[@]}" up -d --no-build --wait api worker web || echo 'Rollback failed; operator intervention required' >&2
  else
    echo 'First deployment failed; stopping new application services and preserving data' >&2
    "${compose[@]}" stop api worker web || true
  fi
  exit "$result"
}

# Build before switching running services. Dependencies retain named volumes.
"${compose[@]}" build api worker web
"${compose[@]}" up -d --wait postgres redis minio
"${compose[@]}" run --rm minio-init
# Future migrations must be backwards-compatible; destructive down is never automatic.
"${compose[@]}" run --rm migrate up
trap rollback ERR
"${compose[@]}" up -d --no-build --wait --wait-timeout 150 api worker web
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18081/health/live
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18080/health/ready
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:18080/ >/dev/null
if [[ -n "$previous_path" && -d "$previous_path" && "$previous_path" != "$release_path" ]]; then
  ln -sfn "$previous_path" "$deploy_root/previous"
fi
ln -sfn "$release_path" "$deploy_root/current"
printf '%s\n' "$release_sha" > "$deploy_root/current-sha"
echo "Deployment healthy: $release_sha"
