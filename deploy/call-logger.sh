#!/bin/sh
# Run Docker Compose against the configured production topology.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
environment_file=${CALL_RECORDER_ENV_FILE:-$root/deploy/.env}
if [ -f "$environment_file" ]; then set -a; . "$environment_file"; set +a; fi

if [ "${CALL_RECORDER_EXTERNAL_POSTGRES:-false}" = true ]; then
  compose_file="$root/deploy/docker-compose.external-postgres.yml"
else
  compose_file="$root/deploy/docker-compose.yml"
fi

if command -v docker-compose >/dev/null 2>&1; then
  exec docker-compose -f "$compose_file" "$@"
fi
if docker compose version >/dev/null 2>&1; then
  exec docker compose -f "$compose_file" "$@"
fi
echo 'Docker Compose is not installed' >&2
exit 1
