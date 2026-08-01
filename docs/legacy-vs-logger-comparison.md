# Legacy Recorder vs. Call Recorder

Updated: 2026-08-01

This is a functional comparison of the public legacy site at `record.renfrewcountyscanner.com` and the current logger at `logger.renfrewcountyscanner.com`. The public legacy Calls workflow was inspected directly. The legacy `/webadmin` area requires Digest authentication, so its private settings and streaming administration are explicitly unverified.

## At-a-glance result

The new logger covers the central legacy workflow—finding, reviewing, and playing calls—and adds the operational platform that the public legacy UI does not expose: authenticated users and roles, secure multi-sender ingestion, transcription, durable workers, retention, notifications, auditing, diagnostics, and Linux/Docker deployment.

The remaining public-UI differences are convenience features rather than blockers to receiving or playing calls. The most visible gaps are time-of-day date filtering, PDF/Excel export buttons, one-click print/share actions, expand-all detail rows, and automatic address links.

## Equivalent or closely matching functions

| Function | Legacy recorder | New logger |
|---|---|---|
| Call browsing | Public tabular call log | Authenticated, responsive call log |
| Free-text search | Call text/search field | Full-text search across metadata, transcripts, and notes |
| Sender/system/site/receiver filters | Multi-select filters | Searchable multi-value filters that refresh from live data |
| Talkgroup and radio filters | Multi-select filters | Searchable multi-value filters with alias labels |
| Call-type filtering | Individual, group, and patch | Group, private, patch, group-class, and patched-only controls |
| Date filtering | Date range and all dates | Single date, from/to dates, and all dates |
| SmartSort | Emergency/priority-first option | Emergency/priority-first option |
| New-call updates | Automatic table updates | Server-sent live updates with pause and queued-call count |
| Continuous playback | Supported | Auto-advance through the current result list |
| Auto-play new calls | Supported | Optional auto-play after a live update, subject to browser autoplay policy |
| Theme | Night-mode preference | Dark/light theme with browser persistence |
| Column selection | Visibility controls | 23 compact-column visibility controls stored in the browser |
| Call metadata | Detailed metadata columns | Comparable normalized metadata plus protection/transcription state |
| Audio playback | Embedded player with seek and volume | Persistent player with previous/next, stop, seek, speed, volume, and keyboard shortcuts |
| Notes | Row details and editing | Authenticated notes editing and notes in search/export |
| CSV export | Supported | Supported for the complete filtered result |
| Bulk audio export | Supported | Filtered TAR audio export with manifest |
| Per-call audio save | Supported | Authenticated audio download |

## Functions improved or added in the new logger

- Site-wide authentication with local accounts, remembered sessions, and admin/editor/viewer roles.
- Optional Cloudflare Access integration and trusted-proxy controls.
- Multiple independently revocable sender credentials; API keys are displayed only at creation or replacement.
- Modern two-stage upload API plus a legacy-compatible Trunk Recorder ingestion endpoint.
- Durable sender-side spool, retry/backoff, idempotency keys, upload expiry, and duplicate-call prevention.
- Explicit upload failure responses and logging instead of silently treating rejected uploads as successful.
- Linux-native Go/PostgreSQL service packaged with Docker Compose; no Windows recorder dependency.
- Live SSE updates that can be paused; paused arrivals are counted and applied on resume.
- Current filter suggestions loaded when a menu opens, server-side option search, and most-recently-seen ordering.
- Full call-detail page with normalized metadata, received text, generated transcript, notes, raw metadata, downloads, and protection controls.
- Talkgroup and radio aliases with source tracking, enabled state, categories, descriptions, and CSV import/export tooling.
- Talkgroup transcription defaults, per-talkgroup language, and explicit administrative opt-out.
- Encrypted transcription API key storage, OpenAI-compatible/faster-whisper provider configuration, queue, retry, status, history, worker diagnostics, limits, and endpoint-network safeguards.
- Favourite groups, members, display aliases, call-list filtering, and favourite flags.
- Retention policies with priority, dry-run preview, sender/system/talkgroup/type/duration matching, protected-call exclusion, purge history, and audio cleanup.
- Call protection with reason, optional expiry, actor, timestamps, effective-expiry handling, and audit history.
- Notification destinations for generic webhooks, Discord, Telegram, and SMTP; rules, retries, history, tests, and worker processing.
- Receiver/sender status, last-call timing, operational health, version/commit/build reporting, and storage-capacity reporting.
- Storage diagnostics for missing database-referenced audio and orphaned files.
- Checksummed backup and restore procedures for PostgreSQL and audio.
- Responsive layouts, accessibility checks, reduced-motion support, keyboard player controls, and print styling.
- JSON export for individual calls and structured health endpoints for monitoring.

## Missing or partial legacy conveniences

| Legacy convenience | Current new-logger status |
|---|---|
| Time-of-day picker in the date range | Partial: the new logger filters whole dates only. |
| PDF export button | Missing; CSV and JSON are available. |
| Excel export button | Missing; CSV opens in spreadsheet applications. |
| Dedicated Print button | Partial: print CSS and the browser print command work, but there is no one-click control. |
| Expand/collapse all row-detail panels | Missing. The old “Show All Rows” expands details. |
| One-click Share/Copy call link | Partial: every detail page has a stable URL, but no copy-to-clipboard button is provided. |
| Street-address detection and map links | Missing. Notes and transcripts are rendered as text. |
| Remember SmartSort, autoplay, continuous playback, page length, and search-panel state | Partial: theme, column choice, pause state, and player preferences persist; not every legacy preference does. |
| Page sizes 10 and 500 | Different: the new choices are 25, 50, 100, 250, and an unbounded result. |
| Legacy “Show All Rows” terminology | Mismatch: the new control means return all matching database rows, not expand detail rows. |
| Public anonymous access | Intentional difference: the new production logger requires authentication. |

## Intentional product-boundary differences

- The new project receives completed calls; it does not decode radio, control SDR hardware, configure trunking systems, or replace Trunk Recorder.
- It does not reproduce Windows recording settings, proprietary branding, vendor assets, or provider-locked services.
- Editing and administrative operations require authorization instead of being exposed through the public call page.
- Audio remains on Linux storage while metadata and state live in PostgreSQL.

## Unverified legacy areas

- The legacy `/webadmin` endpoint responds with HTTP Digest authentication and was not accessed during this audit.
- Legacy authenticated recorder, SDR, streaming, user, storage, and service controls therefore cannot be claimed as present or absent.
- No private credentials, settings, or recordings were used for this comparison.

