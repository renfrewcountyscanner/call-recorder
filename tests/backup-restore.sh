#!/bin/sh
# Self-contained backup/restore acceptance test using the production
# deploy/backup.sh and deploy/restore.sh scripts. It starts the production
# docker-compose.yml stack (so the scripts see the exact same configuration)
# with a temporary runtime/ symlink, seeds a call, backs it up, simulates data
# loss, restores, and verifies the call and its media are back.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
export COMPOSE="${COMPOSE:-docker-compose}"
export CALL_RECORDER_ENV_FILE="$root/deploy/integration.env"
# backup.sh and restore.sh intentionally read deployment settings from their
# environment; export the isolated fixture settings for those child processes.
set -a
. "$root/deploy/integration.env"
set +a

project="callrecorder_it"
backup_dest=$(mktemp -d)
work=$(mktemp -d)
runtime_saved=""
export COMPOSE_PROJECT_NAME="callrecorder_it"

restore_runtime_link() {
  rm -f "$root/runtime"
  if [ -n "$runtime_saved" ] && [ -d "$runtime_saved/runtime" ]; then
    mv "$runtime_saved/runtime" "$root/runtime"
  fi
  rm -rf "$runtime_saved"
}

cleanup() {
  status=$?
  restore_runtime_link
  rm -rf "$backup_dest" "$work"
  $COMPOSE --project-name "$project" --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$root/.test-runtime"
  exit "$status"
}
trap cleanup EXIT

# Ensure a clean start even if a previous run left state behind.
rm -rf "$root/.test-runtime"

# The production scripts and compose file use runtime/. Move any existing
# runtime/ aside and point it at our temporary .test-runtime for the test.
if [ -e "$root/runtime" ] || [ -L "$root/runtime" ]; then
  runtime_saved=$(mktemp -d)
  mv "$root/runtime" "$runtime_saved/runtime"
fi
mkdir -p "$root/.test-runtime"
ln -s "$root/.test-runtime" "$root/runtime"

# Start the production stack. Use the integration env file for non-secret
# settings (port, bootstrap sender, keys) while keeping runtime/ pointed at
# .test-runtime via the symlink.
$COMPOSE --project-name "$project" --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" up -d --build >/dev/null 2>&1
ready=0
for n in $(seq 1 60); do
  if curl -fsS --max-time 2 http://127.0.0.1:18080/healthz >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
test "$ready" = 1

# Seed a call via the API.
metadata='{"sender_id":"integration-sender","idempotency_key":"backup-1","audio_format":"wav","call":{"source_call_id":"backup-1","start_time":"2026-01-02T03:04:05Z","duration_ms":1000,"system_id":"system-backup","system_name":"System Backup","site_id":"site-backup","site_name":"Site Backup","talkgroup_id":"900","talkgroup_name":"Backup TG","radio_id":"800","radio_name":"Unit 800","frequency":"851.0125","call_type":"group"}}'
response=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "$metadata" http://127.0.0.1:18080/api/v1/uploads)
token=$(printf '%s' "$response" | sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p')
test -n "$token"
python3 - "$work/call.wav" <<'PY'
import struct, sys, wave
with wave.open(sys.argv[1], "wb") as audio:
    audio.setnchannels(1)
    audio.setsampwidth(2)
    audio.setframerate(8000)
    audio.writeframes(struct.pack("<h", 0) * 8000)
PY
curl -fsS -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/v1/uploads/$token" >/dev/null
id=$($COMPOSE --project-name "$project" --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc 'SELECT id FROM calls LIMIT 1')
test -n "$id"

backup_dir=$("$root/deploy/backup.sh" "$backup_dest")
test -f "$backup_dir/manifest.txt"
test -f "$backup_dir/postgres.dump"
test -f "$backup_dir/audio.tar.gz"
test -f "$backup_dir/SHA256SUMS"
(cd "$backup_dir" && sha256sum -c SHA256SUMS)

# Simulate data loss.
rm -rf "$root/.test-runtime/audio"
$COMPOSE --project-name "$project" --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null

# Restore into the existing stack.
CONFIRM_RESTORE=YES "$root/deploy/restore.sh" "$backup_dir"

# Restart backend so it re-opens restored audio.
$COMPOSE --project-name "$project" --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" up -d --no-deps --force-recreate backend >/dev/null 2>&1
for n in $(seq 1 30); do
  if curl -fsS --max-time 2 http://127.0.0.1:18080/healthz >/dev/null 2>&1; then break; fi
  sleep 1
done

# Verify the restored data.
count=$($COMPOSE --project-name "$project" --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc 'SELECT count(*) FROM calls')
test "$count" = 1
test "$(curl -s -o /dev/null -w '%{http_code}' -H 'Range: bytes=0-3' "http://127.0.0.1:18080/media/$id")" = 206

echo 'backup/restore tests passed'
