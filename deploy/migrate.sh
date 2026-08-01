#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -f "$root/deploy/.env" ]; then
  set -a
  . "$root/deploy/.env"
  set +a
fi

if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = "true" ]; then
  : "${CALL_RECORDER_DATABASE_URL:?set CALL_RECORDER_DATABASE_URL for external PostgreSQL migrations}"
  for migration in "$root"/backend/migrations/*.sql; do
    docker run --rm --network host -i -e CALL_RECORDER_DATABASE_URL postgres:17 \
      sh -c 'psql "$CALL_RECORDER_DATABASE_URL" -v ON_ERROR_STOP=1 -f /dev/stdin' \
      < "$migration"
  done
  exit 0
fi

for migration in "$root"/backend/migrations/*.sql; do
  docker-compose -f "$root/deploy/docker-compose.yml" exec -T postgres psql -U "${POSTGRES_USER:-call_recorder}" -d "${POSTGRES_DB:-call_recorder}" -f /dev/stdin < "$migration"
done
