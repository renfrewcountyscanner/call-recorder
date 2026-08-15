#!/bin/sh
set -eu
umask 077
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${CALL_RECORDER_ENV_FILE:-$root/deploy/.env}
if [ -f "$environment_file" ]; then set -a; . "$environment_file"; set +a; fi

destination=${1:?usage: backup.sh DESTINATION_DIRECTORY}
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="$destination/call-logger-$timestamp"
audio_root=${CALL_RECORDER_AUDIO_HOST_PATH:-$root/audio}
secrets_root=${CALL_RECORDER_SECRETS_HOST_PATH:-$root/runtime/secrets}
audio_mode=${CALL_RECORDER_BACKUP_AUDIO_MODE:-archive}
case "$audio_mode" in archive|inventory) ;; *) echo 'CALL_RECORDER_BACKUP_AUDIO_MODE must be archive or inventory' >&2; exit 2;; esac
test -d "$audio_root" || { echo "audio directory not found: $audio_root" >&2; exit 2; }
mkdir -p "$backup"

writers='backend notification-worker transcription-worker dataset-worker'
restart_writers() { "$root/deploy/call-logger.sh" up -d $writers >/dev/null 2>&1 || true; }
trap restart_writers EXIT HUP INT TERM
"$root/deploy/call-logger.sh" stop $writers >/dev/null

if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = true ]; then
  : "${CALL_RECORDER_DATABASE_URL:?set CALL_RECORDER_DATABASE_URL for external PostgreSQL backups}"
  docker run --rm --network host postgres:17-alpine pg_dump "$CALL_RECORDER_DATABASE_URL" -Fc > "$backup/postgres.dump"
else
  "$root/deploy/call-logger.sh" exec -T postgres pg_dump -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" -Fc > "$backup/postgres.dump"
fi
test -s "$backup/postgres.dump"

find "$audio_root" -type f -printf '%P\t%s\n' | LC_ALL=C sort > "$backup/audio-inventory.tsv"
audio_parent=$(dirname "$audio_root")
audio_name=$(basename "$audio_root")
(cd "$audio_parent" && find "$audio_name" -type f -print0 > "$backup/audio-files.list")

# Audio files are immutable after ingestion. Snapshot the exact file list while
# writers are stopped, then bring ingestion back before copying that list.
restart_writers
trap - EXIT HUP INT TERM

audio_archive=''
if [ "$audio_mode" = archive ]; then
  tar -C "$audio_parent" --null -T "$backup/audio-files.list" -czf "$backup/audio.tar.gz"
  test -s "$backup/audio.tar.gz"
  audio_archive=audio.tar.gz
fi

secrets_archive=''
if [ -d "$secrets_root" ]; then
  tar -C "$(dirname "$secrets_root")" -czf "$backup/secrets.tar.gz" "$(basename "$secrets_root")"
  secrets_archive=secrets.tar.gz
fi
deployment_archive=''
if [ -f "$environment_file" ]; then
  tar -C "$root/deploy" -czf "$backup/deployment.tar.gz" .env docker-compose.yml docker-compose.external-postgres.yml
  deployment_archive=deployment.tar.gz
fi

git_commit=$(git -C "$root" rev-parse HEAD 2>/dev/null || echo unknown)
cat > "$backup/manifest.txt" <<EOF
format=call-logger-backup-v3
created_utc=$timestamp
git_commit=$git_commit
database_mode=$(if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = true ]; then echo external; else echo local; fi)
postgres_dump=postgres.dump
audio_mode=$audio_mode
audio_archive=$audio_archive
audio_inventory=audio-inventory.tsv
secrets_archive=$secrets_archive
deployment_archive=$deployment_archive
EOF

(cd "$backup" && sha256sum postgres.dump audio-inventory.tsv audio-files.list manifest.txt ${audio_archive:+$audio_archive} ${secrets_archive:+$secrets_archive} ${deployment_archive:+$deployment_archive} > SHA256SUMS && sha256sum -c SHA256SUMS)
printf '%s\n' "$backup"
