# Trunk Recorder sender

Copy `example.env` outside Git, restrict it to mode 600, and invoke `upload_call.py --env /etc/call-recorder-uploader.env --audio "$1" --metadata "$2"` from Trunk Recorder's upload script hook. Every completed call is written to a durable local spool before network delivery. Run `--drain` periodically with systemd or cron to retry pending items. The uploader logs sanitized delivery errors to stderr and exits nonzero while pending or failed spool items remain, so service monitoring can detect delivery failures. Audio source files are never deleted.

Set optional `SECONDARY_*` variables to deliver the same call independently to a second destination. A spool item remains pending until every configured destination succeeds; retries are duplicate-safe at the server.

## Legacy directory importer

`import_directory.py` recursively scans `/logger-import` when you run it manually. It imports validated `.mp3` and `.wav` files through the legacy API, archives audio and matching `.json` sidecars only after a successful upload, and removes empty source subdirectories. Files younger than two minutes are left alone so a copy can finish; failed or unrecognized files remain for review and are logged. Set `IMPORT_SUCCESS_ACTION=keep` to retain sources in place or `delete` for the old destructive behavior.

Configure `/etc/call-recorder-import.env` with `DESTINATION_URL`, `UPLOAD_ID`, and `UPLOAD_KEY`. The manual runner canonicalizes known old source labels after each scan: Pembroke P25 calls become `SCANNER-DIGITAL`/`BCD996P2`, conventional sources become `SCANNER-ANALOG`/`BCT15X`, and SEARS and LANARK retain their canonical identities. Install with `install-import.sh`, create `/logger-import`, then run `/usr/local/lib/call-recorder/run-import.sh` whenever you want an import. It prints progress live and appends it to `/var/log/call-recorder-import.log`; follow old output with `tail -f /var/log/call-recorder-import.log`. Keep credentials outside Git.

To follow everything in one terminal, run `monitor-all-logs.sh` from the deployment directory (or set `COMPOSE_FILE=/path/to/docker-compose.yml`). It supports both `docker compose` and the older `docker-compose` command and includes importer journal logs when the importer service is installed.
