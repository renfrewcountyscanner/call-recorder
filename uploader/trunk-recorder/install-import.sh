#!/bin/sh
set -eu

prefix=/usr/local/lib/call-recorder
install -d -m 0750 "$prefix" /logger-import
install -m 0755 import_directory.py "$prefix/import_directory.py"
install -m 0755 run-import.sh "$prefix/run-import.sh"
install -m 0755 canonicalize-import.sh "$prefix/canonicalize-import.sh"
if [ ! -e /etc/call-recorder-import.env ]; then
  cat >&2 <<'EOF'
Create /etc/call-recorder-import.env (mode 600) with:
DESTINATION_URL=https://logger-api.example.invalid
UPLOAD_ID=your-import-sender
UPLOAD_KEY=your-private-sender-key
SYSTEM_NAME=your-system-id
RECEIVER_ID=your-receiver-id
EOF
fi
echo "Run manually with: $prefix/run-import.sh"
echo "Follow previous import activity with: tail -f /var/log/call-recorder-import.log"
