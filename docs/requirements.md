# Product Requirements

## Purpose and scope

Provide a Linux-hosted, clean-room call archive for completed calls. The first release ingests, validates, stores, searches, plays, deduplicates, and retains calls. It excludes SDR hardware control, radio decoding, and trunking-system control.

## Required capabilities

1. Receive completed calls from Trunk Recorder-compatible sources and multiple remote recorders.
2. Accept metadata before audio, associate the two safely, and retain the original source timestamp.
3. Store normalized call metadata and alias data in PostgreSQL; store audio files separately with a relative media path.
4. Serve authenticated, paginated search and byte-range audio playback.
5. Support individual and continuous playback from a filtered/sorted result set.
6. Manage talkgroup, radio-user, site, system, and sender aliases.
7. Detect duplicate submissions with configurable time/duration tolerances.
8. Retry transient outbound/integration failures through durable job records.
9. Apply global and per-group retention rules safely.

## Non-goals for first release

Live radio streaming, email notification, transcription, external publishing, certificate automation, SQL Server support, SDR control, and radio decoding.
