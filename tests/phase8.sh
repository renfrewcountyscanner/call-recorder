#!/bin/sh
# Phase 8 synthetic smoke coverage. Never uses runtime/ or external destinations.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="${COMPOSE:-docker-compose} --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
cleanup(){ $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime"; }
trap cleanup EXIT
KEEP_TEST_ENV=1 "$root/tests/integration.sh"
psql(){ $compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "$1"; }
ready=0
for n in $(seq 1 60); do
  if curl -fsS --max-time 2 http://127.0.0.1:18080/healthz >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
test "$ready" = 1
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/005_phase8_notifications_transcription.sql >/dev/null
group=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "INSERT INTO favourite_groups(name) VALUES('synthetic-favourites') RETURNING id" | grep -E '^[0-9]+$' | head -n 1)
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "INSERT INTO favourite_members(group_id,system_id,talkgroup_id) VALUES($group,'system-a','100')" >/dev/null
test "$(psql "SELECT count(*) FROM favourite_members WHERE group_id=$group")" = 1
curl -fsS "http://127.0.0.1:18080/calls?favourite=$group" | grep -q 'Dispatch'
call_id=$(psql 'SELECT id FROM calls ORDER BY start_time LIMIT 1')
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "UPDATE calls SET protected=true,protection_reason='synthetic test' WHERE id='$call_id'" >/dev/null
test "$(psql "SELECT protected FROM calls WHERE id='$call_id'")" = t
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "INSERT INTO notification_destinations(name,destination_type,config) VALUES('synthetic-fake','webhook','{\"url\":\"http://127.0.0.1:9/fake\"}')" >/dev/null
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "INSERT INTO transcription_jobs(call_id,provider) SELECT '$call_id',provider FROM transcription_config WHERE id=true ON CONFLICT DO NOTHING" >/dev/null
test "$(psql "SELECT count(*) FROM transcription_jobs WHERE call_id='$call_id'")" = 1
echo 'phase 8 synthetic tests passed'
