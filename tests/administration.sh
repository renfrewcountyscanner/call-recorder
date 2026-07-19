#!/bin/sh
# Web administration smoke coverage; only callrecorder_it/.test-runtime are used.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="docker-compose --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
work=$(mktemp -d)
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
trap cleanup EXIT
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio"
CALL_RECORDER_ADMIN_ENABLED=true CALL_RECORDER_ADMIN_TOKEN=synthetic-admin-token $compose up -d --build
for n in $(seq 1 40); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
curl -fsS -c "$work/cookie" -d 'token=synthetic-admin-token' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/login | grep -q 303
curl -fsS -b "$work/cookie" -d 'system=system-z&id=900&alias=Manual+Dispatch&description=synthetic&category=test&priority=4&source=manual&enabled=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/talkgroups | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select alias from talkgroup_aliases where system_id='system-z' and talkgroup_id='900'")" = 'Manual Dispatch'
curl -fsS -b "$work/cookie" -d 'name=synthetic-policy&retention_days=30&priority=1&dry_run=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/retention | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select count(*) from retention_policies where name='synthetic-policy' and enabled=false and dry_run=true")" = 1
policy_id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select id from retention_policies where name='synthetic-policy'")
curl -fsS -b "$work/cookie" -d "id=$policy_id&name=synthetic-policy-updated&retention_days=31&priority=2&dry_run=on" -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/retention | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select retention_days from retention_policies where id=$policy_id")" = 31
curl -fsS -b "$work/cookie" http://127.0.0.1:18080/admin/retention | grep -q 'synthetic-policy'
echo 'administration tests passed'
