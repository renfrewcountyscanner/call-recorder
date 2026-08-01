# Call Recorder

## v0.4.2

This release adds real notification test delivery, storage diagnostics CLI, inline transcript display in the call list, and an interactive install script. It builds on v0.4.1 which added favourite groups, retention-protected calls, durable notification deliveries, and an optional OpenAI-compatible transcription queue.

See [CHANGELOG.md](CHANGELOG.md) for the full history and [known limitations](docs/known-limitations.md) for remaining work.

Call Recorder is a Linux-native call logger for receiving completed calls from Linux Trunk Recorder installations. It uses Go, PostgreSQL, Docker Compose, bind-mounted Linux audio storage, durable sender spooling, browser playback, and verified backup/restore tooling.

A clean-room Linux call logger for completed radio calls. The scope is: ingest completed calls from Trunk Recorder and remote recorder sources, store normalized metadata and audio in PostgreSQL-backed storage, and provide secure browser search and playback.

This project does not include radio decoding, SDR control, trunking-system control, proprietary installer material, decompiled code, vendor artwork, or call recordings.

## Quick install

Run the installer from the repository root. It prompts for the directory where call recordings should be stored, generates secure secrets, and starts the Docker Compose stack:

```bash
git clone https://github.com/renfrewcountyscanner/call-recorder.git
cd call-recorder
./install.sh
```

The installer supports two PostgreSQL modes:
- **Local container** (default) — deploys PostgreSQL in Docker
- **External server** — connects to an existing PostgreSQL instance

See [docs/linux-installation.md](docs/linux-installation.md) for detailed installation instructions.

## What's included

- Direct Linux Trunk Recorder ingestion
- Modern and legacy-compatible upload APIs
- Multiple authenticated senders with durable retry
- MP3/WAV validation and storage
- Browser playback with sequential mode
- Filtering, sorting, SmartSort, and CSV/JSON exports
- SSE live updates with pause/resume
- Aliases with CSV import/export
- Favourites with call-list filtering
- Protected calls with expiry and audit history
- Retention and purging with dry-run preview
- Notifications (SMTP, webhook, Discord, Telegram)
- End-to-end transcription with encrypted API key
- Inline transcripts in the call list
- Site-wide browser authentication with local admin/editor/viewer accounts and 30-day remembered sessions
- Administrator storage-capacity gauge for the audio filesystem
- Backup and restore with checksums
- Storage diagnostics CLI (missing/orphan audio reports)
- Build commit and timestamp reporting in health endpoint

## Repository layout

- `backend/` — Go server and Linux administration command
- `uploader/` — Trunk Recorder sender components
- `deploy/` — deployment templates without secrets
- `tests/` — integration, browser, accessibility, and retention tests
- `docs/` — design documentation and readiness matrix

## Clean-room boundary

The documentation records observed interoperability requirements, not copied implementation. Do not add installers, extracted artifacts, decompiled source, proprietary branding/assets, secrets, production databases, or identifiable recordings.
