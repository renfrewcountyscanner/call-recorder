# Changelog

## v1.0.0 - 2026-08-02

- Made new talkgroups inherit enabled transcription and added explicit inherit/on/off policies with per-talkgroup language overrides.
- Made call filter menus live, system-aware, and safe for duplicate talkgroup/radio IDs across systems; search now includes corrected/generated transcripts.
- Hardened modern ingestion with strict metadata validation, idempotent create/upload responses, atomic upload leases, retryable status fields, receiver heartbeat data, and safe automatic aliases.
- Added reversible stale receiver dismissal, restore controls, audit entries, and an administrator-configurable stale threshold.
- Added transcript edit/review states, provider/model/language provenance, abandoned-job recovery, bounded retries, and effective transcript display.
- Added asynchronous, snapshot-based speech-to-text dataset ZIP exports with original audio, JSONL manifests, checksums, deterministic train/validation/test splits, warnings, cancellation, expiry, and administrator-only download.
- Added a compact mobile call-card layout, bottom navigation, responsive player, PWA manifest/service worker, and Media Session controls.
- Strengthened local authentication with opaque server-side sessions, live role/enable checks, revocation on password/disable, safer redirects, POST logout, login throttling reset, and last-administrator protection.
- Added worker heartbeat/recovery improvements, terminal notification retry limits, effective transcript keyword delivery, and retention protection for active exports.
- Made retention delete the exact selected call IDs, recheck protection/export state, tolerate missing files with recorded failures, and report matched bytes/duration.
- Added automatic Compose migrations, export storage, dataset workers, installer support, and AGPL-3.0-or-later licensing.

## Unreleased

- Fixed newly discovered talkgroups so transcription defaults on at both the database and ingestion layers while preserving administrator opt-outs.
- Made Calls-page sender/system/site/receiver/talkgroup/radio choices refresh from current call data when opened, with server-side option search and recent-first suggestions.
- Made filter labels show identifiers plus names, raised the option capacity, and use sender identity when FleetNet omits `receiver_id`.
- Fixed sender deletion to revoke and archive credentials referenced by historical calls instead of returning a foreign-key error; archived senders no longer appear in active filter choices.
- Changed compact call-detail values to yellow while keeping transcript text in the normal foreground colour.
- Added a verified legacy comparison and a self-contained logger feature inventory.
- Added an optional site-wide browser authentication gate with local admin/viewer sessions and trusted Cloudflare Access identity mapping.
- Added login throttling, explicit viewer/admin separation, and a visible sign-in flow.
- Added an administrator-only current free-space gauge for the configured audio filesystem.
- Stopped the installer from printing sender and administrator secrets.

## v0.4.2 (2026-07-31)

- Injected git commit and build timestamp into `/healthz` response and Docker images.
- Added backend Docker healthcheck; fixed transcription-worker healthcheck to actually run `diagnose`.
- Added favourite group indicators (★) on call list and call detail pages.
- Added `/admin/logout` endpoint to clear the admin session cookie.
- Fixed alias `notification_eligible` checkbox: unchecked now correctly saves as `false`.
- Fixed alias edit-fill: source field no longer silently converts "received" to "manual".
- Fixed protected-call badge: expired protections no longer show "Protected".
- Added `install.sh` for local deployment with interactive prompts for audio path, PostgreSQL mode, and admin settings.
- Added `docker-compose.external-postgres.yml` for deployments using an external PostgreSQL server.
- Added searchable DataTables-style filter dropdowns for sender/system/site/receiver/talkgroup/radio.
- Made call list columns sortable via header links.
- Aligned Administration dropdown under the button (fixed clipping and positioning).
- Set default container timezone to `America/Toronto`.

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
