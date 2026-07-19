# Call Recorder

A clean-room Linux call logger for completed radio calls. The initial scope is deliberately narrow: ingest completed calls from Trunk Recorder and remote recorder sources, store normalized metadata and audio in PostgreSQL-backed storage, and provide secure browser search and playback.

This project does not include radio decoding, SDR control, trunking-system control, proprietary installer material, decompiled code, vendor artwork, or call recordings.

## Initial scope

- Receive completed calls from multiple sources.
- Store metadata, aliases, media references, ingestion state, and retention policy in PostgreSQL.
- Store and play MP3/WAV media.
- Search calls, play individual calls, and continuously play filtered calls.
- Manage talkgroup/radio-user aliases and prevent duplicate uploads.

See [docs/requirements.md](docs/requirements.md) and [docs/architecture.md](docs/architecture.md).

## Repository layout

- `docs/` — clean-room interoperability and design documentation.
- `backend/` — future server implementation.
- `frontend/` — future browser client.
- `uploader/` — future sender/remote-ingestion components.
- `deploy/` — deployment templates without secrets.
- `tests/` — future synthetic fixtures and tests only.

## Clean-room boundary

The documentation records observed interoperability requirements, not copied implementation. Do not add installers, extracted artifacts, decompiled source, proprietary branding/assets, secrets, production databases, or identifiable recordings.
