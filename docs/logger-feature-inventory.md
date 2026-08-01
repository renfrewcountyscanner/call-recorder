# Call Recorder: Complete Feature Inventory

Updated: 2026-08-01

This document is a self-contained description of the logger for planning, support, and copying into another ChatGPT conversation. It intentionally contains no API keys, passwords, private endpoints, production database values, or real call content.

## Product purpose and boundary

Call Recorder is a Linux-native logger for completed radio calls produced by Trunk Recorder or another authorized sender. It accepts metadata and MP3/WAV audio, normalizes and stores the call, and provides authenticated browser search, playback, transcription, retention, notifications, and administration.

It is not an SDR application. It does not decode radio signals, select frequencies, manage trunking systems, control receiver hardware, or contain proprietary recorder code and artwork.

## Architecture and deployment

- Go backend serving the browser UI, ingestion APIs, exports, media, health endpoints, and administrative workflows.
- PostgreSQL for calls, pending uploads, aliases, users, sessions, transcription, notifications, favourites, retention, and audit records.
- Audio stored as files on a bind-mounted Linux filesystem; the database stores normalized relative paths, size, format, and SHA-256 digest.
- Docker Compose services for the backend, PostgreSQL when used locally, transcription worker, and optional notification worker.
- External PostgreSQL mode supported; migrations are applied explicitly with `deploy/migrate.sh`.
- Interactive installer creates secrets, supports local or external PostgreSQL, prepares storage, and starts the stack.
- Application version, source commit, and build timestamp exposed through health data and the UI.
- Time zone is configurable; production displays local call times while APIs retain normalized timestamps.

## Call ingestion

- Modern two-stage API: authenticated metadata registration followed by authenticated audio upload using a short-lived upload token.
- Legacy-compatible endpoint for Trunk Recorder upload workflows.
- Multiple remote senders, each with its own credential and enabled/disabled state.
- Sender IDs are validated and API keys are stored as secure hashes.
- New or replacement API keys are displayed once; existing keys cannot be recovered from the application.
- Pending uploads record metadata, expected format, expiry, idempotency key, status, and completed call reference.
- MP3 and WAV formats supported with content-type and size validation.
- Configurable maximum upload size, pending-upload lifetime, and duplicate tolerances.
- Calls are finalized transactionally: database failure removes the newly written audio, and file-finalization failure rolls back the database work.
- Explicit non-success HTTP responses and sender-side logging expose rejected metadata/audio instead of silently losing calls.

## Sender reliability and duplicate handling

- Trunk Recorder uploader integration forwards completed calls from each receiver installation.
- Durable local sender spool keeps failed work for retry instead of discarding it.
- Retry/backoff behavior handles temporary logger or network failures.
- Idempotency keys prevent repeated metadata registration from creating repeated calls.
- Sender/source-call uniqueness and metadata/time/duration similarity checks guard against duplicate audio from retries or overlapping feeds.
- Completed and duplicate responses provide the canonical call identifier.
- Receiver, sender, system, site, talkgroup, radio, frequency, call type, patches, timestamps, and audio characteristics are normalized into the call model.

## Calls browser and live operation

- Responsive compact call table intended to fit many calls on desktop and mobile screens.
- Primary call values are yellow; transcript content remains white in the dark theme.
- Generated transcripts use natural height rather than a fixed collapsed preview.
- Server-sent events update the list as calls arrive.
- Pause/resume control queues an arrival count while paused and refreshes on resume.
- If audio is playing, updates wait until playback will not disrupt the user.
- Optional auto-play for a newly arrived call, subject to browser autoplay permission.
- Result count, filter chips, pagination, first/last navigation, and empty/error states.
- Page-size choices of 25, 50, 100, 250, or all matching rows.
- Print-specific styling for useful paper/PDF output through the browser.

## Search and filtering

- Full-text search across identifiers, names, sites, receivers, frequencies, call type, notes, received text, and transcripts.
- Multi-value sender, system, site, receiver, talkgroup, and radio filters.
- Dynamic option menus fetch current database values whenever opened.
- Option search is performed server-side with a debounce, so values outside the initial suggestion limit remain findable.
- Initial suggestions are bounded and ordered by most recent call, keeping newly received systems, talkgroups, receivers, and radios visible.
- Existing selected values remain present even when old, outside the limit, or outside the current menu search.
- Filter option fields are server allowlisted and search values are parameterized.
- Favourite-group, frequency, call-type, group/private class, minimum/maximum duration, single-date, date-range, all-dates, and patched-only filters.
- Typed query-string values continue to work and filtered views remain linkable.
- SmartSort can prioritize emergency/high-priority activity.
- Sorts include newest, oldest, talkgroup ID/label, radio ID/label, duration, frequency, system, site, call type, LCN, and receiver.

## Call-list columns and metadata

- Browser-local column preferences for time, date, target ID/label/tag, source ID/label/tag, duration, voice type, call type, site ID/label, system ID/label/type, audio start, LCN, frequency, receiver, notes/text, filename, and flags.
- Columns can be restored to defaults.
- Flags identify protection, favourites, notes, received text, generated transcripts, transcription queue/failure, and patches.
- Each row exposes additional compact details without leaving the call list.
- Alias joins preserve original call identifiers while presenting current enabled labels.

## Playback and downloads

- Persistent HTML audio player across the call-list page.
- Play/pause, previous, next, stop, seek, playback speed, and volume controls.
- Auto-advance/continuous playback through the current result sequence.
- Keyboard shortcuts for play/pause, previous, next, stop, and mute, ignored while typing in a form.
- Per-call configured audio offset support.
- HTTP byte-range media delivery enables seeking.
- Individual audio download, individual JSON export, filtered CSV export, and filtered bulk-audio TAR export with a manifest.

## Call detail and notes

- Stable detail URL for every call.
- Displays sender, receiver, system/site, talkgroup, radio, frequency, LCN, voice service, call type/class, patches, start time, duration, audio details, and stored metadata.
- Shows received call text separately from generated transcription where available.
- Authorized editors can add or change administrative notes.
- Notes and transcripts participate in search.
- Raw normalized metadata is retained for diagnostics/export without exposing secrets.

## Talkgroups and radios

- Talkgroup and radio alias records are keyed by system plus identifier.
- Alias, description, category, enabled state, source, timestamps, call count, and latest activity are shown administratively.
- Sources distinguish automatically received, manually maintained, and imported records.
- Automatically received names update received-source aliases without overwriting manual ownership.
- Talkgroups additionally carry priority, transcription enabled/language, and notification-eligibility settings.
- New talkgroups default to transcription enabled at both the schema and ingestion layers.
- An administrator may disable transcription; later received calls do not reverse that choice.
- Search, edit, and CSV import/export administration are available.

## Transcription

- Optional OpenAI-compatible and faster-whisper-compatible transcription providers.
- Encrypted-at-rest transcription API key managed through the WebUI.
- Provider endpoint, model, language, temperature, VAD, phrase prompt, duration/file limits, timeout, concurrency, retry limit, and allowed endpoint network ranges are configurable.
- Configuration can be enabled independently from worker processing.
- Provider connectivity test and diagnostics are available.
- Eligible calls are automatically queued based on global configuration, talkgroup opt-in, available audio, size/duration limits, and configured secret.
- Per-talkgroup enable/disable and language override, including bulk administration.
- Jobs track pending/running/succeeded/failed state, attempts, scheduling, errors, and provider.
- Failed jobs can be retried without erasing their prior attempt count/history.
- Transcripts are stored separately, copied into search data, shown inline in the call list, and displayed on call details.
- Worker can run continuously in Docker or once from the administration CLI.

## Favourites

- Named favourite groups with enabled state and display order.
- Group membership uses system/talkgroup pairs and may have a display alias.
- Administration suggests talkgroups already observed in calls.
- Calls can be filtered by favourite group and flagged with matching groups.
- Favourite membership can be used by notification rules.

## Retention and protected calls

- Retention policies include name, enabled state, priority, number of days, and dry-run mode.
- Optional conditions include sender, system, talkgroup, call type, and duration range.
- Preview shows matching calls and estimated audio impact before deletion.
- Purging removes eligible database rows and associated audio with recorded run history.
- Calls can be protected from retention with a reason, actor, timestamp, and optional expiry.
- Expired protection stops being effective without destroying the audit record.
- Protection changes are recorded for administrative traceability.
- Retention history records policy, counts, bytes, timing, and errors.

## Notifications

- Destination types: generic JSON webhook, Discord, Telegram, and SMTP email.
- Destinations are created disabled and can be tested before activation.
- Rules can filter on sender, system, site, talkgroup, radio, call type, frequency range, duration range, patched-only state, text keyword, and favourite group.
- Rules have priority, enabled state, destination, and customizable template.
- Deliveries are durable records with pending/running/succeeded/failed state, attempts, last/next attempt, success time, and error.
- Failed or pending deliveries can be retried from history.
- Notification worker supports continuous processing and diagnostic/test workflows.

## Users, sessions, and authorization

- Local usernames and password authentication.
- Argon2-based password hashing.
- Admin, editor, and viewer roles.
- Viewer access covers calls, media, exports, status, and live/filter endpoints.
- Editors can maintain call data and operational settings permitted to their role.
- Administrators manage users, sender credentials, storage diagnostics, and other privileged operations.
- Remember-me sessions can last 30 days; sessions are stored server-side and cookies use secure production settings.
- Site-wide authentication can be required, with a configurable login URL.
- Optional Cloudflare Access identity integration uses an explicit trusted-proxy configuration.

## Status, diagnostics, and observability

- `/healthz` and `/readyz` endpoints report service status and build information.
- Receiver/sender status page shows recent activity and helps identify stale or missing feeds.
- Structured server logs cover authentication, legacy ingestion outcomes, upload failures, transcription enqueue failures, and worker behavior.
- Storage page reports filesystem capacity and usage for the audio root.
- Storage diagnostic CLI reports database rows whose audio is missing and audio files with no database row.
- Notification, retention, transcription, and protection histories support operational investigation.

## Backup and restore

- Deployment scripts back up PostgreSQL plus the audio tree.
- Backup manifests and checksums detect incomplete or corrupted archives.
- Restore tooling validates input and documents service-order considerations.
- Local-container and external-PostgreSQL deployments have explicit migration/backup handling.
- Secrets and live production data are intentionally excluded from source control.

## Security and data protections

- API keys are hashed; application secrets such as the transcription provider key are encrypted with a filesystem master key.
- The master key is generated with restricted permissions and mounted only where needed.
- SQL filters use parameterized values; dynamic filter fields use a strict allowlist.
- Role checks protect administrative writes and sensitive diagnostics.
- Audio download paths are derived from stored normalized paths rather than arbitrary user filesystem input.
- Upload tokens expire and are stored as hashes.
- Endpoint/network restrictions reduce transcription-provider SSRF risk.
- Templates use Go HTML escaping; dynamically loaded option labels are inserted with DOM `textContent`.
- Repository policy excludes credentials, production databases, recordings, proprietary artifacts, and vendor assets.

## Current limitations and planning notes

- The logger depends on an external recorder such as Trunk Recorder to produce calls.
- Browser autoplay may require prior user interaction.
- Date filtering is whole-day only; there is no time-of-day picker.
- There are no dedicated PDF or Excel export buttons; CSV and browser printing are available.
- There is no one-click copy/share button, automatic address-to-map linking, or expand-all-detail control.
- “Show All Rows” can return a large unbounded result and should be used carefully.
- Some UI preferences are stored locally or per session, but not every search/playback control is persisted.
- Legacy authenticated `/webadmin` features have not been audited because access was unavailable.

