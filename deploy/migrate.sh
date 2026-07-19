#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
docker-compose -f "$root/deploy/docker-compose.yml" exec -T postgres psql -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" -f /dev/stdin < "$root/backend/migrations/002_aliases_retention.sql"
