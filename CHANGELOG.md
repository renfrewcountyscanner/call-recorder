# Changelog

## v0.4.1 (2026-07-30)

- Added explicit pause/resume live updates with queued new-call indication.
- Added safe bounded call-list page sizes: 25, 50, 100, and 250.
- Added print-friendly call-list and call-detail output that omits controls and administration actions.
- Preserved existing ingestion, PostgreSQL storage, playback, aliases, retention, notifications, and transcription behavior.

## v0.4.0 (2026-07-30)

- Additive Phase 8 schema for favourite groups, protected calls, notifications, and transcription jobs.
- Admin surfaces and Linux CLI commands for favourite groups, protection, notification history, and transcription queueing.
- Optional notification and transcription worker profiles; both are disabled by default.
- Generated transcript storage remains separate from received sender text and administrative notes.

## v0.3.0 (2026-07-26)

- Redesigned web interface: dark-first theme system with light and system options, shared layout and navigation, and a small internal design system.
- Scanner-oriented call log: alias-primary rows with secondary ID chips, call-type and patch badges, responsive stacked cards on mobile, total counts, and pagination.
- Filters: from/to date range, removable active-filter chips, reset, preserved values, and shareable query-string URLs.
- Playback: single shared player bar with previous/next/stop, seek and time display, 0.75×–2× speed (session-persisted), volume, auto-advance toggle, row highlighting, and keyboard controls.
- Sectioned call-detail page with collapsible pretty-printed metadata.
- Redesigned talkgroup, radio, retention, login, and unauthorized pages; retention history page; confirmation dialog for destructive actions; obvious enabled/dry-run badges.
- Self-contained assets: vendored HTMX replaces the CDN, strict Content-Security-Policy, cacheable static files.
- Automated axe-core accessibility suite, browser acceptance suite, and screenshot capture in the isolated environment.

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
