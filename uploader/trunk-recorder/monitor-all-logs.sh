#!/bin/sh
# Follow logger Docker logs and the optional legacy-import timer in one view.
set -eu

compose_file=""
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
for candidate in "${COMPOSE_FILE:-}" ./docker-compose.yml ./deploy/docker-compose.yml "$script_dir/../../../deploy/docker-compose.yml" /app/call-recorder/deploy/docker-compose.yml /opt/call-recorder/deploy/docker-compose.yml; do
    if [ -n "$candidate" ] && [ -f "$candidate" ]; then compose_file="$candidate"; break; fi
done

compose_cmd=""
if docker compose version >/dev/null 2>&1; then
    compose_cmd="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
    compose_cmd="docker-compose"
fi

children=""
cleanup() {
    trap - INT TERM EXIT
    [ -z "$children" ] || kill $children 2>/dev/null || true
}
trap cleanup INT TERM EXIT

compose_usable=false
if [ -n "$compose_cmd" ] && [ -n "$compose_file" ]; then
    compose_dir=$(dirname "$compose_file")
    compose_name=$(basename "$compose_file")
    if (cd "$compose_dir" && $compose_cmd -f "$compose_name" config >/dev/null 2>&1); then
        compose_usable=true
        (cd "$compose_dir" && $compose_cmd -f "$compose_name" logs -f --tail=100) &
        children="$!"
        echo "Following Docker Compose logs from $compose_file" >&2
    fi
fi
if [ "$compose_usable" = false ]; then
    [ -n "$compose_cmd" ] && echo "No usable Compose project found; following running containers instead." >&2
    [ -n "$compose_cmd" ] || echo "Compose unavailable; following all running containers." >&2
    for container in $(docker ps -q); do
        name=$(docker inspect --format '{{.Name}}' "$container" | sed 's#^/##')
        (docker logs -f --tail=100 "$container" 2>&1 | sed "s/^/[$name] /") &
        children="$children $!"
    done
fi

import_log=${CALL_RECORDER_IMPORT_LOG:-/var/log/call-recorder-import.log}
if [ -f "$import_log" ]; then
    (tail -n 100 -f "$import_log" 2>&1 | sed 's/^/[importer] /') &
    children="$children $!"
else
    echo "Importer log not created yet; it will appear after the first manual import." >&2
fi

wait
