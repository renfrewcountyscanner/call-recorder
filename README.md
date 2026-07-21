# Call Recorder

## v0.2.0

This release adds system-scoped talkgroup and radio alias administration, CSV import/export, a call-detail page, and safe-by-default retention policies. Retention policies are disabled and dry-run by default; destructive execution is available only through the protected Linux admin command.

Call Recorder is an initial working Linux-native release for receiving completed calls from Linux Trunk Recorder installations. It uses Go, PostgreSQL, Docker Compose, bind-mounted Linux audio storage, durable sender spooling, browser playback, and verified backup/restore tooling. See [CHANGELOG.md](CHANGELOG.md) and [known limitations](docs/known-limitations.md).

A clean-room Linux call logger for completed radio calls. The initial scope is deliberately narrow: ingest completed calls from Trunk Recorder and remote recorder sources, store normalized metadata and audio in PostgreSQL-backed storage, and provide secure browser search and playback.

This project does not include radio decoding, SDR control, trunking-system control, proprietary installer material, decompiled code, vendor artwork, or call recordings.

## Proven local startup

This is Linux-native: Docker runs PostgreSQL; the Go backend and Go uploader run without Windows, Wine, MSSQL, PowerShell, or .NET. Trunk Recorder remains responsible for recording and decoding. Call Recorder receives completed calls only.

```bash
cd deploy
cp example.env .env
# Set strong, private POSTGRES_PASSWORD and CALL_RECORDER_BOOTSTRAP_SENDER_KEY values.
docker-compose config -q
docker-compose up --build -d
docker-compose ps
```

See [docs/development.md](docs/development.md) for sender provisioning and synthetic ingestion.

Runtime PostgreSQL and audio data are bind-mounted under `runtime/postgres` and `runtime/audio` (excluded from Git). Use `deploy/backup.sh DESTINATION_DIRECTORY` to create a verified PostgreSQL dump plus separate audio archive; `deploy/restore.sh` requires `CONFIRM_RESTORE=YES`.

## Initial scope

- Receive completed calls from multiple sources.
- Store metadata, aliases, media references, ingestion state, and retention policy in PostgreSQL.
- Store and play MP3/WAV media.
- Search calls, play individual calls, and continuously play filtered calls.
- Manage system-scoped talkgroup/radio aliases, import/export aliases as CSV, and prevent duplicate uploads.
- Define disabled-by-default retention policies; preview them in the web UI and run them from the Linux admin command.

See [docs/requirements.md](docs/requirements.md) and [docs/architecture.md](docs/architecture.md).

## Repository layout

- `docs/` — clean-room interoperability and design documentation.
- `backend/` — Go server and Linux administration command.
- `uploader/` — Trunk Recorder sender components.
- `deploy/` — deployment templates without secrets.
- `tests/` — isolated synthetic integration, retention, and browser tests.

## Clean-room boundary

The documentation records observed interoperability requirements, not copied implementation. Do not add installers, extracted artifacts, decompiled source, proprietary branding/assets, secrets, production databases, or identifiable recordings.
