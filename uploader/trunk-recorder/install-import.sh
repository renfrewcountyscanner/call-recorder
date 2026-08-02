#!/bin/sh
set -eu

prefix=/usr/local/lib/call-recorder
install -d -m 0750 "$prefix" /logger-import
install -m 0755 import_directory.py "$prefix/import_directory.py"
install -m 0644 systemd/call-recorder-import.service /etc/systemd/system/call-recorder-import.service
install -m 0644 systemd/call-recorder-import.timer /etc/systemd/system/call-recorder-import.timer
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
systemctl daemon-reload
systemctl enable --now call-recorder-import.timer
systemctl status --no-pager call-recorder-import.timer
