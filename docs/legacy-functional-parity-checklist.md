# Legacy functional parity checklist

Source evidence: sanitized public-site inspection, `docs/legacy-webui-parity-audit.md`,
and live browser verification performed for the legacy and Linux URLs. This file
is the working source of truth; every item ends with an explicit status.

Status values: Missing, Partial, Complete, Better in Logger, User rejected, Blocked.

## Public call workflow

| Area | Legacy workflow | Logger status | Evidence / required work |
|---|---|---|---|
| Group/favourite filter | Searchable group selector | Partial | Favourite filter exists; improve multi-select/search workflow |
| Talkgroup/radio/site/system/receiver filters | Searchable multi-selects | Partial | Server filters exist; add multi-value controls |
| Call type | Individual/group/patch choices | Partial | Manual filter exists; expose known choices |
| Date range/all dates | Date-range picker and all-dates toggle | Partial | From/to fields work; add clearer range UX |
| Frequency/duration/patch/text search | Combined filtering | Complete | Query-string filters and PostgreSQL search present |
| SmartSort | Opt-in priority/recent ordering | Partial | Linux now offers an opt-in emergency/priority-first PostgreSQL ordering; live comparison still required |
| Continuous playback | Queue playback | Better in Logger | Persistent player, previous/next, speed and queue exist |
| Auto-play new calls | Optional new-call autoplay | User rejected | Do not autoplay without an explicit user gesture |
| Pause live updates | Pause/resume and queued count | Complete | Live toggle and queued indication present |
| Page size/show-all | Bounded sizes and show-all behavior | Partial | 25/50/100/250 exist; implement safe large-result workflow |
| Sorting | Sortable meaningful columns | Partial | Server-side newest/oldest/talkgroup/radio/duration/frequency sorting added; remaining displayed columns still required |
| Column visibility/order | User-configurable grid | Missing | Add browser-local column preferences |
| Export/print | CSV/Excel/PDF/print/audio | Partial | CSV/JSON/audio/print present; assess useful export gaps |
| Call detail | Full metadata and actions | Partial | Detail exists; compare all fields and actions |

## Administration

| Area | Legacy workflow | Logger status | Evidence / required work |
|---|---|---|---|
| General | Application/general settings | Blocked | Authenticated legacy access required |
| Recording | Recording configuration | Better in Logger | Trunk Recorder remains the recorder; document equivalent metadata |
| Call Import | Import workflows | Better in Logger | Linux ingestion APIs/uploader exist |
| Purging | Purge rules and storage status | Partial | Retention exists; compare storage/orphan diagnostics |
| Webserver | Webserver settings | Better in Logger | Docker/reverse-proxy deployment documented |
| Favourites | Group/membership management | Partial | CRUD exists; improve assisted membership selection |
| Call Uploading | Destinations, retries and status | Partial | Sender and durable queues exist; compare visible controls |
| Streaming | Stream definitions and queue controls | Blocked | Must inspect authenticated usage before exclusion |
| Metadata | Metadata options | Partial | Metadata preserved; compare configurable behavior |
| Email | SMTP and recipient settings | Partial | Notification destinations exist; authenticated parity required |
| Transcribe | Providers, phrases and queue | Partial | Provider abstraction exists; authenticated legacy fields blocked |
| Advanced | Diagnostics and advanced settings | Blocked | Authenticated legacy access required |

## Completion rule

No item is complete until the behavior is usable through the WebUI and covered
by an isolated browser or integration test. No proprietary implementation or
asset is copied.
