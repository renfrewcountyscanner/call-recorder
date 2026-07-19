#!/bin/sh
set -eu
umask 077
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="$root/deploy/docker-compose.yml"
destination=${1:?usage: backup.sh DESTINATION_DIRECTORY}
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="$destination/call-recorder-$timestamp"
mkdir -p "$backup"
git_commit=$(git -C "$root" rev-parse HEAD)
docker-compose -f "$compose" exec -T postgres pg_dump -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" -Fc > "$backup/postgres.dump"
tar -C "$root/runtime" -czf "$backup/audio.tar.gz" audio
test -s "$backup/postgres.dump"
test -s "$backup/audio.tar.gz"
sha256sum "$backup/postgres.dump" "$backup/audio.tar.gz" > "$backup/SHA256SUMS"
cat > "$backup/manifest.txt" <<EOF
format=call-recorder-backup-v1
created_utc=$timestamp
git_commit=$git_commit
postgres_dump=postgres.dump
audio_archive=audio.tar.gz
EOF
sha256sum -c "$backup/SHA256SUMS"
printf '%s\n' "$backup"
