#!/usr/bin/env bash
# Manually import files from /logger-import and write a durable activity log.
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
env_file=${CALL_RECORDER_IMPORT_ENV:-/etc/call-recorder-import.env}
log_file=${CALL_RECORDER_IMPORT_LOG:-/var/log/call-recorder-import.log}

if [[ ! -r "$env_file" ]]; then
    echo "Importer configuration not found: $env_file" >&2
    exit 2
fi

set -a
. "$env_file"
set +a

mkdir -p "$(dirname -- "$log_file")"
touch "$log_file"
echo "$(date -Is) manual import started" | tee -a "$log_file"
python3 "$script_dir/import_directory.py" --root "${IMPORT_ROOT:-/logger-import}" "$@" 2>&1 | tee -a "$log_file"
echo "$(date -Is) manual import finished" | tee -a "$log_file"
