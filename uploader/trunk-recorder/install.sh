#!/bin/sh
set -eu
install -d -m 750 /var/lib/call-recorder-uploader/pending /var/lib/call-recorder-uploader/failed
echo 'Install example.env as /etc/call-recorder-uploader.env with mode 600, then configure Trunk Recorder to invoke upload_call.py.'
