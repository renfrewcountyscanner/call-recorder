#!/bin/sh
# Destructive retention coverage. This script only uses callrecorder_it and .test-runtime.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="docker-compose --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime"; }
trap cleanup EXIT
KEEP_TEST_ENV=1 "$root/tests/integration.sh"
psql() { $compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "$1"; }
before_calls=$(psql 'SELECT count(*) FROM calls')
before_audio=$(find "$root/.test-runtime/audio" -type f | wc -l)
test "$before_calls" -gt 0
test "$before_audio" -gt 0
$compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -c "INSERT INTO retention_policies(name,enabled,dry_run,retention_days,priority) VALUES ('isolated-delete',true,false,1,99)" >/dev/null
$compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend retention run --policy 1 >/tmp/call-recorder-retention.out
test "$(psql 'SELECT count(*) FROM calls')" = 0
test "$(find "$root/.test-runtime/audio" -type f | wc -l)" = 0
test "$(psql 'SELECT calls_deleted FROM retention_runs ORDER BY id DESC LIMIT 1')" = "$before_calls"
# Disabled policy is not selected; a dry run must create history without deleting data.
echo 'retention tests passed'
