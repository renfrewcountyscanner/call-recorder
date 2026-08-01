#!/bin/sh
# Site-wide authentication smoke coverage; isolated callrecorder_it only.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="${COMPOSE:-docker-compose} --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
work=$(mktemp -d)
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
trap cleanup EXIT
$compose down -v --remove-orphans >/dev/null 2>&1 || true
rm -rf "$root/.test-runtime"
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio" "$root/.test-runtime/secrets"
CALL_RECORDER_AUTH_REQUIRED=true CALL_RECORDER_SESSION_COOKIE_SECURE=false CALL_RECORDER_ADMIN_ENABLED=true $compose up -d --build
for n in $(seq 1 60); do curl -fsS --max-time 2 http://127.0.0.1:18080/healthz >/dev/null 2>&1 && break || true; sleep 1; done
$compose exec -T backend /usr/local/bin/call-recorder-admin users create --username admin --password testpassword --role admin
$compose exec -T backend /usr/local/bin/call-recorder-admin users create --username viewer --password viewpassword --role viewer
status=$(curl -s -o "$work/root" -w '%{http_code}' http://127.0.0.1:18080/)
test "$status" = 302
grep -q '/login' "$work/root"
curl -fsS http://127.0.0.1:18080/login | grep -q 'Sign in'
curl -fsS -c "$work/admin-cookie" -d 'username=admin&password=testpassword&return=/' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/login | grep -q 303
curl -fsS -b "$work/admin-cookie" http://127.0.0.1:18080/ | grep -q 'Calls'
curl -fsS -b "$work/admin-cookie" http://127.0.0.1:18080/admin/storage | grep -q 'Audio filesystem capacity'
curl -fsS -c "$work/viewer-cookie" -d 'username=viewer&password=viewpassword&return=/' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/login | grep -q 303
curl -fsS -b "$work/viewer-cookie" http://127.0.0.1:18080/ | grep -q 'Calls'
test "$(curl -s -o /dev/null -w '%{http_code}' -b "$work/viewer-cookie" http://127.0.0.1:18080/admin/storage)" = 403
# Health and sender ingestion remain machine endpoints, not browser-session gated.
curl -fsS http://127.0.0.1:18080/healthz | grep -q '"status"'
echo 'authentication tests passed'
