#!/bin/sh
set -eu
umask 077
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${CALL_RECORDER_ENV_FILE:-$root/deploy/.env}
if [ -f "$environment_file" ]; then set -a; . "$environment_file"; set +a; fi
if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = true ]; then
  echo 'External PostgreSQL backups must use the database provider snapshot procedure before this file/archive backup.' >&2
  exit 2
fi
compose="$root/deploy/docker-compose.yml"
if [ -n "${COMPOSE:-}" ]; then docker_compose=$COMPOSE
elif command -v docker-compose >/dev/null 2>&1; then docker_compose=docker-compose
elif docker compose version >/dev/null 2>&1; then docker_compose='docker compose'
else echo 'Docker Compose is not installed' >&2; exit 1
fi
destination=${1:?usage: backup.sh DESTINATION_DIRECTORY}
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="$destination/call-recorder-$timestamp"
mkdir -p "$backup"
writers='backend notification-worker transcription-worker dataset-worker'
restart_writers() { $docker_compose -f "$compose" up -d $writers >/dev/null 2>&1 || true; }
trap restart_writers EXIT HUP INT TERM
$docker_compose -f "$compose" stop $writers >/dev/null
if git -C "$root" rev-parse HEAD >/dev/null 2>&1; then
  git_commit=$(git -C "$root" rev-parse HEAD)
else
  git_commit=unknown
fi
$docker_compose -f "$compose" exec -T postgres pg_dump -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" -Fc > "$backup/postgres.dump"
tar -C "$root/runtime" -czf "$backup/audio.tar.gz" audio
if [ -d "$root/runtime/secrets" ]; then tar -C "$root/runtime" -czf "$backup/secrets.tar.gz" secrets; fi
test -s "$backup/postgres.dump"
test -s "$backup/audio.tar.gz"
if [ -f "$backup/secrets.tar.gz" ]; then sha256sum "$backup/postgres.dump" "$backup/audio.tar.gz" "$backup/secrets.tar.gz" > "$backup/SHA256SUMS"; else sha256sum "$backup/postgres.dump" "$backup/audio.tar.gz" > "$backup/SHA256SUMS"; fi
cat > "$backup/manifest.txt" <<EOF
format=call-logger-backup-v2
created_utc=$timestamp
git_commit=$git_commit
postgres_dump=postgres.dump
audio_archive=audio.tar.gz
secrets_archive=$(if [ -f "$backup/secrets.tar.gz" ]; then echo secrets.tar.gz; else echo ''; fi)
EOF
find "$root/runtime/audio" -type f -printf '%P\t%s\n' | LC_ALL=C sort > "$backup/audio-inventory.tsv"
sha256sum "$backup/audio-inventory.tsv" >> "$backup/SHA256SUMS"
sha256sum -c "$backup/SHA256SUMS" >&2
restart_writers
trap - EXIT HUP INT TERM
printf '%s\n' "$backup"
