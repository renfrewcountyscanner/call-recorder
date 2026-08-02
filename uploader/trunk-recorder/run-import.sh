#!/usr/bin/env bash
# Manually import files from /logger-import and write a durable activity log.
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
env_file=${CALL_RECORDER_IMPORT_ENV:-/etc/call-recorder-import.env}
log_file=${CALL_RECORDER_IMPORT_LOG:-/var/log/call-recorder-import.log}

if [[ -r "$env_file" ]]; then
    set -a
    . "$env_file"
    set +a
else
    # Running from this repository on the logger host: reuse the existing
    # private legacy-ingestion credential instead of forcing a second config.
    deployment_env="$script_dir/../../deploy/.env"
    if [[ ! -r "$deployment_env" ]]; then
        echo "Importer configuration not found: $env_file" >&2
        echo "Create it, or run this script from the Call Recorder repository." >&2
        exit 2
    fi
    set -a
    . "$deployment_env"
    set +a
    export DESTINATION_URL="${DESTINATION_URL:-http://127.0.0.1:${CALL_RECORDER_PORT:-8080}}"
    export UPLOAD_ID="${UPLOAD_ID:-${CALL_RECORDER_LEGACY_AUTH_ID:-}}"
    export UPLOAD_KEY="${UPLOAD_KEY:-${CALL_RECORDER_LEGACY_API_KEY:-}}"
    if [[ -z "$UPLOAD_ID" || -z "$UPLOAD_KEY" ]]; then
        echo "The deployment environment does not contain legacy import credentials." >&2
        exit 2
    fi
    echo "Using the logger deployment's legacy import credential." >&2
fi

mkdir -p "$(dirname -- "$log_file")"
touch "$log_file"
echo "$(date -Is) manual import started" | tee -a "$log_file"
set +e
python3 "$script_dir/import_directory.py" --root "${IMPORT_ROOT:-/logger-import}" "$@" 2>&1 | tee -a "$log_file"
import_result=${PIPESTATUS[0]}
set -e
"$script_dir/canonicalize-import.sh" 2>&1 | tee -a "$log_file"
echo "$(date -Is) manual import finished" | tee -a "$log_file"
exit "$import_result"
