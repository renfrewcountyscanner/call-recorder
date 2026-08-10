#!/bin/sh
set -eu
umask 077
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${CALL_RECORDER_ENV_FILE:-$root/deploy/.env}
if [ -f "$environment_file" ]; then set -a; . "$environment_file"; set +a; fi
if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = true ]; then
  echo 'External PostgreSQL restores require the database provider restore procedure and are not performed by this script.' >&2
  exit 2
fi
compose="$root/deploy/docker-compose.yml"
if [ -n "${COMPOSE:-}" ]; then docker_compose=$COMPOSE
elif command -v docker-compose >/dev/null 2>&1; then docker_compose=docker-compose
elif docker compose version >/dev/null 2>&1; then docker_compose='docker compose'
else echo 'Docker Compose is not installed' >&2; exit 1
fi
backup=${1:?usage: CONFIRM_RESTORE=YES restore.sh BACKUP_DIRECTORY}
test "${CONFIRM_RESTORE:-}" = YES || { echo 'set CONFIRM_RESTORE=YES to restore' >&2; exit 2; }
test -f "$backup/manifest.txt" && test -f "$backup/postgres.dump" && test -f "$backup/audio.tar.gz" && test -f "$backup/SHA256SUMS"
grep -Eqx 'format=(call-recorder-backup-v1|call-logger-backup-v2)' "$backup/manifest.txt"
(cd "$backup" && sha256sum -c SHA256SUMS)

python3 "$root/deploy/validate-archive.py" "$backup/audio.tar.gz"
if [ -f "$backup/secrets.tar.gz" ]; then python3 "$root/deploy/validate-archive.py" "$backup/secrets.tar.gz"; fi

# Determine the runtime UID/GID from the backend container instead of hard-coding 10001.
container_uid=$($docker_compose -f "$compose" exec -T backend id -u 2>/dev/null || echo 10001)
container_gid=$($docker_compose -f "$compose" exec -T backend id -g 2>/dev/null || echo 10001)

staging=$(mktemp -d "$root/runtime/.restore.XXXXXX")
staging_secrets=""
cleanup_staging() {
  rm -rf "$staging"
  if [ -n "$staging_secrets" ]; then rm -rf "$staging_secrets"; fi
}
trap cleanup_staging EXIT
tar -C "$staging" -xzf "$backup/audio.tar.gz"
test -d "$staging/audio"
if [ -f "$backup/secrets.tar.gz" ]; then
  staging_secrets=$(mktemp -d "$root/runtime/.secrets-restore.XXXXXX")
  tar -C "$staging_secrets" -xzf "$backup/secrets.tar.gz"
  test -f "$staging_secrets/secrets/master.key"
  chmod 600 "$staging_secrets/secrets/master.key"
fi

# Stop application writers only after every archive and staged path has been
# validated. PostgreSQL remains available for pg_restore.
$docker_compose -f "$compose" stop backend notification-worker transcription-worker dataset-worker >/dev/null
$docker_compose -f "$compose" exec -T postgres pg_restore -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" --clean --if-exists --exit-on-error < "$backup/postgres.dump"

rollback_tag=$(date -u +%Y%m%dT%H%M%SZ)
old_audio="$root/runtime/audio.before-restore-$rollback_tag"
if [ -e "$root/runtime/audio" ]; then
  mv "$root/runtime/audio" "$old_audio"
else
  old_audio=""
fi
mv "$staging/audio" "$root/runtime/audio"
chown -R "${container_uid}:${container_gid}" "$root/runtime/audio"
chmod 750 "$root/runtime/audio"
if [ -f "$backup/secrets.tar.gz" ]; then
  old_secrets="$root/runtime/secrets.before-restore-$rollback_tag"
  if [ -e "$root/runtime/secrets" ]; then
    mv "$root/runtime/secrets" "$old_secrets"
  else
    old_secrets=""
  fi
  mv "$staging_secrets/secrets" "$root/runtime/secrets"
  chown -R "${container_uid}:${container_gid}" "$root/runtime/secrets"
  chmod 750 "$root/runtime/secrets"
  rm -rf "$staging_secrets"
fi
$docker_compose -f "$compose" up -d migrate backend notification-worker transcription-worker dataset-worker
if [ -n "$old_audio" ]; then
  printf 'Restore completed. Previous audio retained at %s\n' "$old_audio"
else
  printf 'Restore completed. No previous audio directory existed.\n'
fi
