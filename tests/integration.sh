#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="docker-compose --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
cleanup() { $compose down >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
work=$(mktemp -d)
trap cleanup EXIT
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio"
$compose up -d --build
for n in $(seq 1 30); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
test "$(curl -s -o "$work/malformed.json" -w '%{http_code}' -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data '{' http://127.0.0.1:18080/api/v1/uploads)" = 400
grep -q 'invalid JSON metadata' "$work/malformed.json"
test "$(curl -s -o "$work/no-key.json" -w '%{http_code}' -H 'Content-Type: application/json' --data '{"sender_id":"integration-sender","audio_format":"wav","call":{}}' http://127.0.0.1:18080/api/v1/uploads)" = 400
test "$(curl -s -o "$work/unknown.json" -w '%{http_code}' -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary 'RIFFxxxxWAVE' http://127.0.0.1:18080/api/v1/uploads/no-such-token)" = 404
grep -q 'upload not found' "$work/unknown.json"
cat > "$work/call.json" <<'EOF'
{"sender_id":"integration-sender","idempotency_key":"fixture-1","audio_format":"wav","call":{"source_call_id":"fixture-1","start_time":"2026-01-02T03:04:05Z","duration_ms":1000,"system_id":"system-a","system_name":"System A","site_id":"site-a","site_name":"Site A","talkgroup_id":"100","talkgroup_name":"Dispatch","radio_id":"200","radio_name":"Unit 200","frequency":"851.0125","call_type":"group","patches":[{"talkgroup_id":"101","talkgroup_name":"Patch"}]}}
EOF
printf 'RIFF\044\000\000\000WAVEfmt \020\000\000\000\001\000\001\000\100\037\000\000\000\076\000\000\002\000\020\000data\000\000\000\000' > "$work/call.wav"
response=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/call.json" http://127.0.0.1:18080/api/v1/uploads)
token=$(printf '%s' "$response" | sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p')
test -n "$token"
curl -fsS -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/v1/uploads/$token" >/dev/null
count=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')
test "$count" = 1
id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT id FROM calls LIMIT 1')
test "$(curl -s -o /dev/null -w '%{http_code}' -H 'Range: bytes=0-3' "http://127.0.0.1:18080/media/$id")" = 206
duplicate=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/call.json" http://127.0.0.1:18080/api/v1/uploads)
printf '%s' "$duplicate" | grep -q '"duplicate":true'
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')" = 1
printf 'ID3synthetic' > "$work/call.mp3"
sed 's/fixture-1/fixture-mp3/g; s/03:04:05Z/03:05:10Z/; s/"wav"/"mp3"/' "$work/call.json" > "$work/mp3.json"
response=$(curl -fsS -H 'Content-Type: application/json' -H 'X-Call-Recorder-Key: synthetic-integration-key' --data-binary "@$work/mp3.json" http://127.0.0.1:18080/api/v1/uploads)
token=$(printf '%s' "$response" | sed -n 's/.*"upload_token":"\([^"]*\)".*/\1/p')
test -n "$token"
curl -fsS -H 'X-Call-Recorder-Sender: integration-sender' -H 'X-Call-Recorder-Key: synthetic-integration-key' -H 'Content-Type: audio/mpeg' --data-binary "@$work/call.mp3" "http://127.0.0.1:18080/api/v1/uploads/$token" >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')" = 2
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
echo 'integration tests passed'
