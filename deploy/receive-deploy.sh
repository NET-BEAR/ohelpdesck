#!/usr/bin/env bash
# Installed by the operator, not replaced by the deployment credential.
set -euo pipefail
umask 077
if [[ ! "${SSH_ORIGINAL_COMMAND:-}" =~ ^deploy\ ([a-f0-9]{40})$ ]]; then
  echo 'Only deploy <40-character commit SHA> is accepted' >&2
  exit 64
fi
release_sha=${BASH_REMATCH[1]}
deploy_root=/opt/ohelpdesck-dev
exec 9>"$deploy_root/deploy.lock"
flock -w 900 9
release_path="$deploy_root/releases/$release_sha"
staging_path=$(mktemp -d "$deploy_root/releases/.staging.XXXXXXXX")
archive_path=$(mktemp "$deploy_root/incoming.XXXXXXXX.tar.gz")
trap 'rm -f "$archive_path"; rm -rf "$staging_path"' EXIT
# Bound uploaded source archive to 64 MiB. Artifacts contain source only.
head -c 67108865 > "$archive_path"
if (( $(stat -c %s "$archive_path") > 67108864 )); then
  echo 'Source archive too large' >&2
  exit 65
fi
python3 - "$archive_path" "$staging_path" <<'PY'
import sys, tarfile
from pathlib import PurePosixPath
with tarfile.open(sys.argv[1], 'r:gz') as archive:
    members = archive.getmembers()
    if sum(item.size for item in members) > 256 * 1024 * 1024:
        raise SystemExit('Expanded archive too large')
    for item in members:
        path = PurePosixPath(item.name)
        if path.is_absolute() or '..' in path.parts or not (item.isfile() or item.isdir()):
            raise SystemExit('Unsafe archive member')
        if any(part in {'.git', '.env', '.ssh'} for part in path.parts):
            raise SystemExit('Forbidden archive member')
    archive.extractall(sys.argv[2], members=members, filter='data')
PY
mkdir -p "$deploy_root/release-checksums"
archive_checksum=$(sha256sum "$archive_path" | cut -d ' ' -f 1)
checksum_path="$deploy_root/release-checksums/$release_sha"
if [[ -e "$release_path" ]]; then
  if [[ ! -f "$checksum_path" || "$(cat "$checksum_path")" != "$archive_checksum" ]]; then
    echo 'Release SHA already exists with different archive content' >&2
    exit 65
  fi
else
  mv "$staging_path" "$release_path"
  printf '%s\n' "$archive_checksum" > "$checksum_path"
fi
bash "$deploy_root/bin/remote-apply" "$release_sha"
