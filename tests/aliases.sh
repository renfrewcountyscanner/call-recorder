#!/bin/sh
# Alias precedence and CSV CLI coverage using synthetic isolated data only.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="docker-compose --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
work=$(mktemp -d)
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
trap cleanup EXIT
KEEP_TEST_ENV=1 "$root/tests/integration.sh"
# A manual system-scoped alias must override received data and remain separate by system.
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "UPDATE talkgroup_aliases SET alias='Manual Dispatch',source='manual' WHERE system_id='system-a' AND talkgroup_id='100'; INSERT INTO talkgroup_aliases(system_id,talkgroup_id,alias,source) VALUES('system-b','100','Other System','received')" >/dev/null
curl -fsS http://127.0.0.1:18080/calls | grep -q 'Manual Dispatch'
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select alias from talkgroup_aliases where system_id='system-b' and talkgroup_id='100'")" = 'Other System'
$compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend aliases talkgroups export > "$work/talkgroups.csv"
grep -q 'system_id,talkgroup_id,alias,description,category,enabled,source' "$work/talkgroups.csv"
echo 'alias tests passed'
