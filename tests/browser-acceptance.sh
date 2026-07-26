#!/bin/sh
# Web interface acceptance coverage; only callrecorder_it/.test-runtime are used.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="docker-compose --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime"; }
trap cleanup EXIT
KEEP_TEST_ENV=1 "$root/tests/integration.sh"
CALL_RECORDER_ADMIN_ENABLED=true CALL_RECORDER_ADMIN_TOKEN=synthetic-admin-token $compose up -d --no-deps --force-recreate backend >/dev/null
for n in $(seq 1 40); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
/usr/bin/python3 "$root/tests/browser-acceptance.py"
