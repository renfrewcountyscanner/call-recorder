#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cleanup() { docker-compose --project-name callrecorder_it --env-file "$root/deploy/integration.env" -f "$root/deploy/docker-compose.yml" -f "$root/deploy/docker-compose.integration.yml" down >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime"; }
trap cleanup EXIT
KEEP_TEST_ENV=1 "$root/tests/integration.sh"
/usr/bin/python3 "$root/tests/browser-sequential.py"
