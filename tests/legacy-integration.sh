#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
project=callrecorder_it
work=$(mktemp -d)
compose="${COMPOSE:-docker-compose} --project-name $project --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"

cleanup() {
  if [ "${KEEP_TEST_ENV:-0}" != 1 ]; then
    $compose down >/dev/null 2>&1 || true
    rm -rf "$root/.test-runtime"
  fi
  rm -rf "$work"
}
trap cleanup EXIT

export CALL_RECORDER_LEGACY_INGESTION_ENABLED=true
export CALL_RECORDER_LEGACY_AUTH_ID=legacy-bootstrap
export CALL_RECORDER_LEGACY_API_KEY=synthetic-legacy-bootstrap
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio"
$compose up -d --build
for n in $(seq 1 40); do
  if curl -fsS http://127.0.0.1:18080/healthz >/dev/null; then break; fi
  if [ "$n" -eq 40 ]; then $compose logs --no-color backend >&2 || true; exit 1; fi
  sleep 1
done

admin_output=$($compose run --rm --no-deps --entrypoint /usr/local/bin/call-recorder-admin backend sender create --name legacy-dynamic)
legacy_key=$(printf '%s\n' "$admin_output" | sed -n 's/^api_key=//p')
test "$(printf '%s' "$legacy_key" | wc -c)" -eq 64

cat >"$work/call.json" <<EOF
{"apiAuthID":"legacy-dynamic","apiKey":"$legacy_key","callAudioFormat":"wav","recordedCall":{"startTime":"2026-01-04T03:04:05Z","callDuration":1.0,"talkGroupInfo":{"callTargets":[{"targetid":900,"targetlabel":"Legacy Test","targettag":"dispatch"}],"receiver":"legacy-test","frequency":851.1,"sourceid":123,"sourcelabel":"Unit 123","sourcetag":"field","lcn":1,"voiceservice":"P25","systemid":"legacy-system","systemlabel":"Legacy System","siteid":"legacy-site","sitelabel":"Legacy Site","calltype":1}}}
EOF
printf 'RIFF\044\000\000\000WAVEfmt \020\000\000\000\001\000\001\000\100\037\000\000\000\076\000\000\002\000\020\000data\000\000\000\000' >"$work/call.wav"

response=$(curl -fsS -H 'Content-Type: application/json' --data-binary "@$work/call.json" http://127.0.0.1:18080/api/callupload)
printf '%s' "$response" | grep -q '"Status":200\|"Status":201'
token=$(printf '%s' "$response" | sed -n 's/.*"CallAudioID":"\([^"]*\)".*/\1/p')
test -n "$token"

audio_response=$(curl -fsS -H 'Content-Type: audio/wav' --data-binary "@$work/call.wav" "http://127.0.0.1:18080/api/callaudioupload/$token")
printf '%s' "$audio_response" | grep -q '"Status":200'

count=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc 'SELECT count(*) FROM calls')
test "$count" -eq 1
test "$(find "$root/.test-runtime/audio" -type f | wc -l)" -eq 1
metadata=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "SELECT system_id || ':' || site_id || ':' || talkgroup_id || ':' || radio_id FROM calls LIMIT 1")
test "$metadata" = 'legacy-system:legacy-site:900:123'
echo 'legacy integration tests passed (dynamic sender and two-stage upload)'
