#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
for migration in "$root"/backend/migrations/*.sql; do
  docker-compose -f "$root/deploy/docker-compose.yml" exec -T postgres psql -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" -f /dev/stdin < "$migration"
done
