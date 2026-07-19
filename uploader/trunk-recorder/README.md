# Trunk Recorder sender

Copy `example.env` outside Git, restrict it to mode 600, and invoke `upload_call.py --env /etc/call-recorder-uploader.env --audio "$1" --metadata "$2"` from Trunk Recorder's upload script hook. Every completed call is written to a durable local spool before network delivery. Run `--drain` periodically with systemd or cron to retry pending items. Audio source files are never deleted.

Set optional `SECONDARY_*` variables to deliver the same call independently to a second destination. A spool item remains pending until every configured destination succeeds; retries are duplicate-safe at the server.
