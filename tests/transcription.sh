#!/bin/sh
# End-to-end transcription test: WebUI configuration -> worker -> fake provider -> transcript.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="${COMPOSE:-docker compose} --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
work=$(mktemp -d)
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
trap cleanup EXIT
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio"
CALL_RECORDER_ADMIN_ENABLED=true CALL_RECORDER_SESSION_SECRET=test-secret-key-1234567890 $compose up -d --build

ready=0
for n in $(seq 1 60); do
  if curl -fsS --max-time 2 http://127.0.0.1:18080/healthz >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
test "$ready" = 1

# Verify the latest migration is idempotent.
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/006_transcription_webui_secrets.sql >/dev/null
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/007_transcription_functional_completion.sql >/dev/null

# Create admin user
$compose exec -T backend /usr/local/bin/call-recorder-admin users create --username admin --password testpassword --role admin
curl -fsS -c "$work/cookie" -d 'username=admin&password=testpassword' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/login | grep -q 303

# Create a synthetic WAV call via the modern API.
meta=$(cat <<'EOF'
{"sender_id":"integration-sender","idempotency_key":"transcription-test-1","audio_format":"wav","call":{"source_call_id":"","start_time":"2026-07-30T12:00:00Z","duration_ms":2500,"receiver_id":"rx1","system_id":"system-a","system_name":"System A","site_id":"site-1","site_name":"Site 1","talkgroup_id":"100","talkgroup_name":"Dispatch","talkgroup_tag":"","radio_id":"2001","radio_name":"","radio_tag":"","frequency":"851.000000","lcn":"","voice_service":"","call_type":"group","group_call":true}}
EOF
)
token=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' -d "$meta" http://127.0.0.1:18080/api/v1/uploads | python3 -c 'import sys,json; print(json.load(sys.stdin)["upload_token"])')
# 0.1 second silent mono WAV.
python3 -c '
import struct, sys
sample_rate = 16000
samples = int(sample_rate * 0.1)
data = b"\x00" * (samples * 2)
size = 36 + len(data)
sys.stdout.buffer.write(b"RIFF" + struct.pack("<I", size) + b"WAVEfmt " + struct.pack("<IHHIIHH", 16, 1, 1, sample_rate, sample_rate*2, 2, 16) + b"data" + struct.pack("<I", len(data)) + data)
' > "$work/synthetic.wav"
call_result=$(curl -fsS -H 'Content-Type: audio/wav' -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/synthetic.wav" "http://127.0.0.1:18080/api/v1/uploads/$token")
call_id=$(printf '%s' "$call_result" | python3 -c 'import sys,json; print(json.load(sys.stdin)["call_id"])')
test -n "$call_id"

# Enable the talkgroup for transcription.
curl -fsS -b "$work/cookie" -d 'system=system-a&id=100&alias=Dispatch&description=synthetic&category=test&priority=4&source=manual&enabled=on&transcription_enabled=on&transcription_language=en' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/talkgroups | grep -q 303

# Configure transcription provider pointing at the fake transcriber.
curl -fsSL -b "$work/cookie" -d 'api_key=fake-key' http://127.0.0.1:18080/admin/transcription/secret >/dev/null
curl -fsS -b "$work/cookie" -d 'enabled=on&processing_enabled=on&provider=fake-transcriber&provider_type=faster-whisper&endpoint=http%3A%2F%2Ffake-transcriber%3A8080%2Fv1%2Faudio%2Ftranscriptions&model=whisper-v3&language=en&min_duration_seconds=0.1&max_duration_minutes=15&max_file_size_mb=50&temperature=0&vad_enabled=on&phrase_prompts_enabled=on&phrase_prompt=radio&request_timeout_seconds=60&concurrency=1&retry_limit=3&allowed_endpoint_cidrs=172.30.0.0%2F24' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/config | grep -q 303

# Test the provider from the WebUI.
curl -fsS -b "$work/cookie" -X POST http://127.0.0.1:18080/admin/transcription/test | grep -q '"ok":true'

# Queue the call manually.
curl -fsS -b "$work/cookie" -X POST -o /dev/null -w '%{http_code}' "http://127.0.0.1:18080/admin/transcription/queue/$call_id" | grep -q 303

# Run the worker once.
$compose exec -T transcription-worker /usr/local/bin/call-recorder-admin transcription run

# Verify the transcript was stored.
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT text FROM transcripts WHERE call_id='$call_id'")" = 'synthetic transcript'

# Verify the complete transcript is searchable and rendered without a collapsed preview control.
transcript_page=$(curl -fsS "http://127.0.0.1:18080/calls?q=synthetic+transcript")
printf '%s' "$transcript_page" | grep -q "$call_id"
printf '%s' "$transcript_page" | grep -q 'synthetic transcript'
if printf '%s' "$transcript_page" | grep -q 'transcript-toggle'; then echo 'transcript was rendered with a collapse control'; exit 1; fi

# Test retry: reset a failed job without erasing history.
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "UPDATE transcription_jobs SET status='failed',error='synthetic failure',attempt_count=2 WHERE call_id='$call_id'" >/dev/null
old_attempts=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT attempt_count FROM transcription_jobs WHERE call_id='$call_id'")
test "$old_attempts" = 2
curl -fsS -b "$work/cookie" -d "id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT id FROM transcription_jobs WHERE call_id='$call_id'")" -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/retry | grep -q 303
new_attempts=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT attempt_count FROM transcription_jobs WHERE call_id='$call_id'")
test "$new_attempts" = 2
$compose exec -T transcription-worker /usr/local/bin/call-recorder-admin transcription run

# Worker diagnose reports healthy.
$compose exec -T transcription-worker /usr/local/bin/call-recorder-admin transcription diagnose | grep -q 'Database connectivity'

# === Talkgroup toggle tests ===
# Create a second talkgroup for testing.
curl -fsS -b "$work/cookie" -d 'system=system-a&id=200&alias=Operations&description=test&category=test&priority=1&source=manual&enabled=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/talkgroups | grep -q 303

# Verify newly created talkgroups have transcription enabled by default.
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "t"

# Administrators can explicitly opt out before later re-enabling the group.
curl -fsS -b "$work/cookie" -d 'action=disable&talkgroup=system-a:200' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/talkgroups/toggle | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "f"

# Bulk enable: enable talkgroup 200 for transcription with language override.
curl -fsS -b "$work/cookie" -d 'action=enable&talkgroup=system-a:200&language=fr' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/talkgroups/update | grep -q 303
# Verify enabled and language set.
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT CASE WHEN transcription_enabled THEN 't' ELSE 'f' END||','||coalesce(transcription_language,'') FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "t,fr"

# Bulk disable: disable talkgroup 200.
curl -fsS -b "$work/cookie" -d 'action=disable&talkgroup=system-a:200' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/talkgroups/update | grep -q 303
# Verify disabled, but language preserved.
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT CASE WHEN transcription_enabled THEN 't' ELSE 'f' END||','||coalesce(transcription_language,'') FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "f,fr"

# Bulk enable without language: preserves existing language.
curl -fsS -b "$work/cookie" -d 'action=enable&talkgroup=system-a:200' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/talkgroups/update | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT CASE WHEN transcription_enabled THEN 't' ELSE 'f' END||','||coalesce(transcription_language,'') FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "t,fr"

# Per-row toggle: disable talkgroup 200 via single toggle.
curl -fsS -b "$work/cookie" -d 'action=disable&talkgroup=system-a:200' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/talkgroups/toggle | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "f"

# Per-row toggle: re-enable talkgroup 200 via single toggle.
curl -fsS -b "$work/cookie" -d 'action=enable&talkgroup=system-a:200' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/transcription/talkgroups/toggle | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='200'")" = "t"

# No selection returns 400.
if curl -fsS -b "$work/cookie" -d 'action=enable' http://127.0.0.1:18080/admin/transcription/talkgroups/update 2>/dev/null; then echo 'expected 400'; exit 1; fi

# Invalid action returns 400.
if curl -fsS -b "$work/cookie" -d 'action=invalid&talkgroup=system-a:200' http://127.0.0.1:18080/admin/transcription/talkgroups/update 2>/dev/null; then echo 'expected 400'; exit 1; fi

# Invalid talkgroup value (no colon) is silently skipped, resulting in 400.
if curl -fsS -b "$work/cookie" -d 'action=enable&talkgroup=invalid' http://127.0.0.1:18080/admin/transcription/talkgroups/update 2>/dev/null; then echo 'expected 400'; exit 1; fi

# Guest cannot perform toggle (no cookie).
if curl -fsS -d 'action=enable&talkgroup=system-a:200' http://127.0.0.1:18080/admin/transcription/talkgroups/update 2>/dev/null; then echo 'expected auth failure'; exit 1; fi

# Verify talkgroup 100 (from earlier) is still enabled and unchanged.
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='100'")" = "t"

echo 'transcription integration tests passed'
