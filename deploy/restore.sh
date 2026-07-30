#!/bin/sh
set -eu
umask 077
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="$root/deploy/docker-compose.yml"
backup=${1:?usage: CONFIRM_RESTORE=YES restore.sh BACKUP_DIRECTORY}
test "${CONFIRM_RESTORE:-}" = YES || { echo 'set CONFIRM_RESTORE=YES to restore' >&2; exit 2; }
test -f "$backup/manifest.txt" && test -f "$backup/postgres.dump" && test -f "$backup/audio.tar.gz" && test -f "$backup/SHA256SUMS"
grep -qx 'format=call-recorder-backup-v1' "$backup/manifest.txt"
(cd "$backup" && sha256sum -c SHA256SUMS)
docker-compose -f "$compose" exec -T postgres pg_restore -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" --clean --if-exists < "$backup/postgres.dump"
staging=$(mktemp -d "$root/runtime/.restore.XXXXXX")
trap 'rm -rf "$staging"' EXIT
tar -C "$staging" -xzf "$backup/audio.tar.gz"
test -d "$staging/audio"
rm -rf "$root/runtime/audio"
mv "$staging/audio" "$root/runtime/audio"
chown -R 10001:10001 "$root/runtime/audio"
chmod 750 "$root/runtime/audio"
if [ -f "$backup/secrets.tar.gz" ]; then
  staging_secrets=$(mktemp -d "$root/runtime/.secrets-restore.XXXXXX")
  tar -C "$staging_secrets" -xzf "$backup/secrets.tar.gz"
  test -f "$staging_secrets/secrets/master.key"
  chmod 600 "$staging_secrets/secrets/master.key"
  rm -rf "$root/runtime/secrets"
  mv "$staging_secrets/secrets" "$root/runtime/secrets"
  chown -R 10001:10001 "$root/runtime/secrets"
  chmod 750 "$root/runtime/secrets"
  rm -rf "$staging_secrets"
fi
