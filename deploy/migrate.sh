#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -f "$root/deploy/.env" ]; then
  set -a
  . "$root/deploy/.env"
  set +a
fi

if command -v docker-compose >/dev/null 2>&1; then
  compose=docker-compose
else
  compose='docker compose'
fi

if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = "true" ]; then
  : "${CALL_RECORDER_DATABASE_URL:?set CALL_RECORDER_DATABASE_URL for external PostgreSQL migrations}"
  $compose -f "$root/deploy/docker-compose.external-postgres.yml" run --rm migrate
else
  $compose -f "$root/deploy/docker-compose.yml" run --rm migrate
fi
