# Changelog

## v0.2.0 (2026-07-20)

- System-scoped talkgroup and radio alias records with received, imported, and manual sources.
- Alias CSV import/export and protected administration pages.
- Resolved aliases in the call list and call-detail view.
- Disabled-by-default retention policies, execution history, PostgreSQL advisory locking, and a Linux admin CLI.
- Retention uses an audio-root-local trash move before database commit to avoid deleting outside managed storage.
- Additive schema migrations and isolated retention coverage.

## v0.1.0

- Linux-native Go backend with PostgreSQL metadata and Linux filesystem audio storage.
- Direct Linux Trunk Recorder ingestion plus modern and legacy-style ingestion endpoints.
- Multiple sender identities, durable sender spool, bounded retry, and failed queue.
- Browser call log, filtering, individual playback, sequential playback, and byte-range media delivery.
- Sender-scoped duplicate prevention, Docker Compose deployment, bind-mounted runtime storage, and backup/restore tooling.
