# Legacy WebUI functional-parity audit

Audit date: 2026-07-30. Scope was read-only Chromium inspection of the public legacy site. No credentials, cookies, request bodies, keys, recordings, proprietary source, or visual assets were retained. Sanitized screenshots are local-only under `test-output/legacy-webui-audit/` and are ignored by Git.

## Complete page inventory

| Legacy area | URL / access | Confirmed controls | Linux equivalent | Parity |
|---|---|---|---|---|
| Call log | `/` public | Group, talkgroup, radio user, site, system, call type, receiver, date range, all-dates, SmartSort, continuous playback, auto-play-new, pause updates, night mode, show-all-rows, export, grid column selection, sorting, pagination, audio player | `/` | Partial |
| Call exports | call-log actions | CSV, Excel, PDF, print, call-audio export | CSV, JSON per call, audio download | Partial |
| Audio player | public header | Play/pause, seek, duration, volume; row selection | persistent player, speed, volume, previous/next, sequential playback | Linux better in queue controls |
| General settings | authenticated | navigation item observed only | no direct equivalent | Cannot verify fields |
| Recording settings | authenticated | navigation item observed only | intentionally out of scope: Trunk Recorder records | Not reproduced |
| Call Import | authenticated | navigation item observed only | modern/legacy ingestion | Superseded |
| Purging | authenticated | navigation item observed only | retention administration | Partial |
| Webserver | authenticated | navigation item observed only | Compose/reverse-proxy configuration | Superseded |
| Favorites | authenticated | navigation item observed only | favourite groups | Partial |
| Call Uploading | authenticated | navigation item observed only | sender administration and upload endpoints | Superseded |
| Streaming | authenticated | navigation item observed only | none | Deferred |
| Metadata | authenticated | navigation item observed only | aliases/call metadata | Partial |
| Email | authenticated | navigation item observed only | notification destinations | Partial |
| Transcribe | authenticated | Call Transcribe, Azure, OpenAI Whisper, Keyword Phrases were requested but authentication was unavailable | transcription administration | Partial |
| Advanced | authenticated | navigation item observed only | no direct equivalent | Cannot verify fields |

Authenticated settings were not inspected because no secure credential variable or authenticated browser profile was available in this audit environment. They are explicitly **cannot verify**, not assumed absent.

## Public call-grid findings

Confirmed visible filter field types: single/multi-select searchable controls for group, talkgroup, radio user, site, system, receiver and call type; a date-range text picker with an all-dates toggle; and boolean toggles for SmartSort, continuous playback, auto-play new calls, pause updates, night mode, and show all rows. Call-type choices include individual, group and patch. The grid exposes time, receiver, system/site, talkgroup, radio user, frequency, LCN, call length, source/target labels and tags, voice type, audio start position, and call notes/text where present. It offers sortable columns, configurable visible columns, page sizes, pagination, print, and CSV/Excel/PDF exports.

## Legacy-to-Linux parity matrix

| Feature | Legacy | Linux | Recommendation |
|---|---|---|---|
| Date/system/site/talkgroup/radio filters | Confirmed | Present | Add selectable known values and multi-select only if needed |
| Receiver/call-type/frequency/duration/text/patch filters | Confirmed | Present | Keep query-string model; improve labels |
| SmartSort | Confirmed | Absent | Evaluate a clean priority/recent-call ordering option |
| Pause updates | Confirmed | New-calls/SSE behavior differs | Add explicit pause toggle |
| Auto-play new calls | Confirmed | Deliberately absent (autoplay safety) | Do not reproduce by default |
| Show all rows / page size | Confirmed | Pagination fixed | Add safe page-size selector |
| Grid column chooser | Confirmed | Absent | Later preference feature |
| Excel/PDF/print | Confirmed | CSV/JSON/audio only | CSV is sufficient first; print stylesheet is low effort |
| Night mode | Confirmed | Dark/light/system themes | Linux better |
| Continuous playback | Confirmed | Present, plus prev/next/speed | Linux better |
| Call notes/text | Confirmed | Notes and received/generated text | Linux better separation |
| Retention/purging | Cannot verify detail | Safe policies, dry-run, protection | Linux safer |
| Transcription providers | Cannot verify detail | OpenAI-compatible provider abstraction | Linux avoids provider lock-in |

## Existing Linux functions that are harder to use

1. Admin navigation is denser than the legacy settings grouping.
2. Page-size and column-preference controls are absent.
3. Favourite membership selection is form-based rather than an assisted picker.
4. Notification/transcription history filtering is basic.
5. Transcription configuration previously exposed raw units; the focused UX branch addresses this.

## Functions missing from Linux

1. SmartSort-style optional ordering.
2. Explicit pause-live-updates control.
3. Safe user-selected page size.
4. User-selectable call-grid columns.
5. Print view and optional spreadsheet/PDF export.
6. Streaming mounts and stream queue controls (deferred).
7. Email recipient management beyond notification destinations.

## Linux functions already better

- Sender-scoped duplicate prevention and durable upload queues.
- PostgreSQL-backed retention dry-runs, protection, audit history, and safe filesystem boundaries.
- Browser playback speed, previous/next controls, byte-range audio, and protected SSE handling.
- Separate raw received metadata, notes, and generated transcripts.
- Linux-native Docker deployment without Windows services or database dependencies.

## Recommended implementation order

1. Add an explicit live-update pause/new-calls indicator.
2. Add safe page-size selection and result-count navigation.
3. Add print-friendly call-list CSS.
4. Evaluate SmartSort as an opt-in query option with documented semantics.
5. Add column preferences only after an authenticated user-preference model exists.
6. Re-audit authenticated legacy settings when securely supplied credentials are available.

## Features that should not be reproduced

- Windows recording, SDR, service, SQLite, SQL Server, or desktop-specific configuration.
- Automatic browser audio playback without a user gesture.
- Provider-specific transcription lock-in.
- Exporting credentials, private paths, upload tokens, or bulk recording archives.
- Legacy branding, assets, source, or visual layout.
