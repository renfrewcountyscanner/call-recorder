#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="${COMPOSE:-docker-compose} --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
cleanup() { if [ "${KEEP_TEST_ENV:-0}" = 1 ]; then return; fi; $compose down >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
work=$(mktemp -d)
trap cleanup EXIT
# Always begin from a clean isolated database and audio root. This never
# touches the production runtime, which is deliberately a different path.
$compose down -v --remove-orphans >/dev/null 2>&1 || true
rm -rf "$root/.test-runtime"
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio" "$root/.test-runtime/secrets"
$compose up -d --build
ready=0
for n in $(seq 1 60); do
  if curl -fsS --max-time 2 http://127.0.0.1:18080/healthz >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
test "$ready" = 1
test -s "$root/.test-runtime/secrets/master.key"
test "$(stat -c '%a' "$root/.test-runtime/secrets/master.key")" = 600
# Create admin user for web UI tests
$compose exec -T backend /usr/local/bin/call-recorder-admin users create --username admin --password testpassword --role admin
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/006_transcription_webui_secrets.sql >/dev/null
test "$(curl -s -o "$work/malformed.json" -w '%{http_code}' -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data '{' http://127.0.0.1:18080/api/v1/uploads)" = 400
grep -q 'invalid JSON metadata' "$work/malformed.json"
test "$(curl -s -o "$work/no-key.json" -w '%{http_code}' -H 'Content-Type: application/json' --data '{"sender_id":"integration-sender","audio_format":"wav","call":{}}' http://127.0.0.1:18080/api/v1/uploads)" = 400
test "$(curl -s -o "$work/unknown.json" -w '%{http_code}' -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary 'RIFFxxxxWAVE' http://127.0.0.1:18080/api/v1/uploads/no-such-token)" = 404
grep -q 'upload not found' "$work/unknown.json"
cat > "$work/call.json" <<'EOF'
{"sender_id":"integration-sender","idempotency_key":"fixture-1","audio_format":"wav","call":{"source_call_id":"fixture-1","start_time":"2026-01-02T03:04:05Z","duration_ms":1000,"system_id":"system-a","system_name":"System A","site_id":"site-a","site_name":"Site A","talkgroup_id":"100","talkgroup_name":"Dispatch","radio_id":"200","radio_name":"Unit 200","frequency":"851.0125","call_type":"group","patches":[{"talkgroup_id":"101","talkgroup_name":"Patch"}]}}
EOF
python3 - "$work/call.wav" <<'PY'
import struct, sys, wave
with wave.open(sys.argv[1], "wb") as audio:
    audio.setnchannels(1)
    audio.setsampwidth(2)
    audio.setframerate(8000)
    audio.writeframes(struct.pack("<h", 0) * 8000)
PY
response=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/call.json" http://127.0.0.1:18080/api/v1/uploads)
token=$(printf '%s' "$response" | sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p')
test -n "$token"
curl -fsS -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/v1/uploads/$token" >/dev/null
count=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')
test "$count" = 1
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT alias FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='100'")" = Dispatch
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='100'")" = t
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT alias FROM radio_aliases WHERE system_id='system-a' AND radio_id='200'")" = 'Unit 200'
# Filter suggestions come from current call data and support remote searching.
curl -fsS 'http://127.0.0.1:18080/filter-options?field=system' | grep -q '"value":"system-a"'
curl -fsS 'http://127.0.0.1:18080/filter-options?field=system' | grep -q 'system-a — System A'
curl -fsS 'http://127.0.0.1:18080/filter-options?field=talkgroup&q=Dispatch' | grep -q '100 — Dispatch'
curl -fsS 'http://127.0.0.1:18080/filter-options?field=receiver' | grep -q '"value":"INTEGRATION-SENDER"'
curl -fsS 'http://127.0.0.1:18080/calls?receiver=INTEGRATION-SENDER' | grep -q 'Dispatch'
curl -fsS 'http://127.0.0.1:18080/filter-options?field=receiver&selected=receiver-not-yet-seen' | grep -q '"value":"receiver-not-yet-seen","label":"receiver-not-yet-seen","selected":true'
test "$(curl -s -o /dev/null -w '%{http_code}' 'http://127.0.0.1:18080/filter-options?field=not-a-field')" = 400
id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT id FROM calls LIMIT 1')
test "$(curl -s -o /dev/null -w '%{http_code}' -H 'Range: bytes=0-3' "http://127.0.0.1:18080/media/$id")" = 206
duplicate=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/call.json" http://127.0.0.1:18080/api/v1/uploads)
printf '%s' "$duplicate" | grep -q '"duplicate":true'
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')" = 1
# An administrator's explicit opt-out must survive later received metadata.
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "UPDATE talkgroup_aliases SET transcription_enabled=false WHERE system_id='system-a' AND talkgroup_id='100'" >/dev/null
sed 's/fixture-1/fixture-second/g; s/03:04:05Z/03:05:10Z/' "$work/call.json" > "$work/second.json"
response=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/second.json" http://127.0.0.1:18080/api/v1/uploads)
token=$(printf '%s' "$response" | sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p')
test -n "$token"
curl -fsS -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/v1/uploads/$token" >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')" = 2
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT transcription_enabled FROM talkgroup_aliases WHERE system_id='system-a' AND talkgroup_id='100'")" = f
before_audio=$(find "$root/.test-runtime/audio" -type f | wc -l)
sed 's/fixture-1/fixture-rollback/g; s/03:04:05Z/03:06:10Z/' "$work/call.json" > "$work/rollback.json"
CALL_RECORDER_TEST_FAIL_FINALIZE=true $compose up -d --no-deps --force-recreate backend
for n in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
response=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/rollback.json" http://127.0.0.1:18080/api/v1/uploads)
token=$(printf '%s' "$response" | sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p')
test -n "$token"
test "$(curl -s -o "$work/rollback-response.json" -w '%{http_code}' -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/v1/uploads/$token")" = 500
grep -q 'test-only finalization failure' "$work/rollback-response.json"
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')" = 2
test "$(find "$root/.test-runtime/audio" -type f | wc -l)" = "$before_audio"
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT count(*) FROM pending_uploads WHERE status='pending'")" -ge 1
CALL_RECORDER_TEST_FAIL_FINALIZE=false $compose up -d --no-deps --force-recreate backend
for n in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
curl -fsS -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/v1/uploads/$token" >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')" = 3
test "$(find "$root/.test-runtime/audio" -type f | wc -l)" = $((before_audio + 1))

# v1 receiver-status controls are reversible and audit their actions.
CALL_RECORDER_ADMIN_OPEN=true $compose up -d --no-deps --force-recreate backend
for n in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "UPDATE receiver_status_entries SET last_call_at=now()-interval '2 hours' WHERE sender_id='INTEGRATION-SENDER' AND system_id='system-a'" >/dev/null
curl -fsS -X POST -d 'sender=INTEGRATION-SENDER&receiver=&system=system-a&site=site-a' http://127.0.0.1:18080/admin/receiver-status/dismiss >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT count(*) FROM receiver_status_entries WHERE sender_id='INTEGRATION-SENDER' AND system_id='system-a' AND dismissed_at IS NOT NULL")" = 1
curl -fsS -X POST -d 'sender=INTEGRATION-SENDER&receiver=&system=system-a&site=site-a' http://127.0.0.1:18080/admin/receiver-status/restore >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT count(*) FROM receiver_status_entries WHERE sender_id='INTEGRATION-SENDER' AND system_id='system-a' AND dismissed_at IS NULL")" = 1

# Dataset exports snapshot effective text and stream the original audio into a
# private ZIP built by the dedicated worker.
dataset_call=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT id FROM calls ORDER BY start_time DESC LIMIT 1")
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "INSERT INTO transcripts(call_id,provider,language,text,original_text,review_status) VALUES('$dataset_call','integration','en','corrected dispatch transcript','generated dispatch transcript','reviewed')" >/dev/null
curl -fsS -X POST -d 'review_status=reviewed' http://127.0.0.1:18080/admin/datasets >/dev/null
dataset_id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT id FROM dataset_exports ORDER BY created_at DESC LIMIT 1')
test -n "$dataset_id"
dataset_ready=0
for n in $(seq 1 30); do
  dataset_status=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT status FROM dataset_exports WHERE id='$dataset_id'")
  if [ "$dataset_status" = completed ] || [ "$dataset_status" = completed_with_warnings ]; then dataset_ready=1; break; fi
  sleep 1
done
test "$dataset_ready" = 1
curl -fsS "http://127.0.0.1:18080/admin/datasets/$dataset_id/download" -o "$work/dataset.zip"
python3 -c 'import json,sys,zipfile; z=zipfile.ZipFile(sys.argv[1]); assert "manifest.jsonl" in z.namelist(); rows=[json.loads(x) for x in z.read("manifest.jsonl").splitlines()]; assert rows and rows[0]["effective_text"]=="corrected dispatch transcript" and rows[0]["split"] in ("train","validation","test")' "$work/dataset.zip"
echo 'integration tests passed'
