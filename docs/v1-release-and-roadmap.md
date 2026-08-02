# Call Recorder v1: release inventory and roadmap

This document is the portable planning reference for the application as of
v1.0.0. It describes what exists, the design rules behind it, and the next
improvements worth considering.

## Product purpose

Call Recorder receives completed radio calls from Trunk Recorder or compatible
senders, safely stores their audio and metadata, and gives listeners and
operators a searchable live call log. It does not decode radio systems or
control SDR hardware.

## Listener experience

- Live calls update through server-sent events and can be paused without
  interrupting playback.
- The shared player supports play/pause, previous/next, seek, volume, speed,
  automatic sequential playback, and optional auto-play of new calls.
- Call details are dense yellow metadata while received/generated transcripts
  remain high-contrast white and grow naturally with their text length.
- Filters cover text, sender, receiver, system, site, talkgroup, radio, call
  type/class, frequency, duration, dates, patches, favourites, and sorting.
- Talkgroup and radio choices are keyed by system plus ID, avoiding collisions
  when two systems reuse the same numeric ID. Menus query current database
  values every time they open.
- Search includes metadata, aliases, received text, and the current corrected
  or generated transcript.
- CSV, call JSON, filtered audio, and direct-call downloads are available.
- Mobile mode uses compact listener cards, a persistent bottom navigation and
  responsive player. The app can be installed as a PWA and publishes Media
  Session metadata/actions for lock-screen controls.

## Ingestion and data integrity

- Modern two-step create/audio upload API plus optional legacy compatibility.
- Sender API keys use Argon2id hashes and can be created, rotated, disabled, or
  archived without deleting historical calls.
- Strict JSON and field-size validation, control-character checks, time/audio
  limits, MP3/WAV header validation, content-length enforcement, and safe paths.
- Idempotency keys, duplicate tolerances, reusable pending responses, completed
  duplicate responses, and atomic upload leases prevent double finalization.
- Pending/uploading/failed state and retryable response fields tell senders what
  actually happened instead of hiding server failures.
- Calls normalize systems, sites, receivers, talkgroups, radios, patches,
  source labels, tags, frequency, LCN, call type, notes, and transcripts.
- Receiver status is updated in the same ingestion transaction as each call.

## Talkgroups, radios and favourites

- Entries are discovered automatically from calls and can be given aliases,
  descriptions and free-form categories.
- Field-level help explains system IDs, entity IDs, display aliases,
  descriptions, categories, priority, visibility, notification eligibility,
  transcription policy, and language overrides.
- Existing systems/categories are suggested without restricting valid new
  values. Provenance (received/manual/imported) is shown but not presented as a
  confusing edit choice.
- New talkgroups inherit the system-wide enabled transcription default. An
  operator can explicitly force transcription on or off per talkgroup.
- Favourite groups support custom membership/labels and call-list filtering.

## Transcription and model-training data

- OpenAI-compatible and Faster-Whisper-compatible providers are supported with
  encrypted API keys, endpoint/CIDR safety, provider tests, size/duration
  limits, VAD, temperature, phrase prompts, concurrency and retry limits.
- The effective language is the talkgroup override followed by the provider
  default. Transcript records retain provider, model and language provenance.
- Jobs are durable, use skip-locked claiming, recover abandoned running work,
  use exponential backoff, and leave terminal failures for explicit retry.
- Received text remains distinct from generated, original and edited text.
  Edits become `unreviewed`; reviewers can mark them reviewed, rejected, inaudible, or no-speech.
- Dataset export takes a transactional snapshot filtered by dates, sender,
  system, talkgroup, language, provider and review status.
- A background worker builds a ZIP containing original audio, `manifest.jsonl`,
  optional `errors.jsonl`, checksums/provenance, effective and source text,
  metadata, and deterministic 90/5/5 train/validation/test splits.
- Exports expose progress, cancellation, warnings, expiry, administrator-only
  downloads, and deletion. Active snapshots are protected from retention.

## Operations, retention and notifications

- Receiver status shows sender/receiver/system/site activity and storage. Stale
  rows may be dismissed and restored; new activity automatically makes a
  dismissed row visible again. The stale threshold is configurable and audited.
- Retention policies filter age and call metadata, support dry-run preview,
  preserve protected calls and active export snapshots, and record matched
  count/bytes/duration.
- Destructive retention stages audio, deletes only the exact selected IDs after
  rechecking protection, restores races, records missing files, and maintains
  run history.
- Calls can be protected indefinitely or until an expiry with reason and audit
  history.
- Webhook, Discord, Telegram and SMTP destinations use durable deliveries,
  bounded retries, SSRF protections and test sends. Keyword rules use effective
  transcripts and are evaluated again when generated text arrives.
- Storage diagnostics identify database/file mismatches and the UI reports
  filesystem capacity.

## Security and deployment

- Viewer/editor/admin roles protect the entire site and administration actions.
- Local login uses Argon2id passwords, opaque hashed server-side sessions,
  SameSite/HttpOnly/optional Secure cookies, live role checks, session
  revocation, brute-force throttling, POST logout and last-admin safeguards.
- Cloudflare Access can be trusted only from configured proxy addresses.
- CSP, anti-framing, MIME-sniffing, referrer and browser-permission headers are
  set centrally. Dangerous UI actions use same-origin POST and confirmation.
- PostgreSQL, audio, encrypted application secrets and exports use persistent
  mounts. Compose applies additive migrations before starting dependent
  services and runs transcription, notification and dataset workers separately.
- The installer supports local or external PostgreSQL, generates secrets,
  configures all persistent paths, builds the stack and waits for health.
- Backup/restore includes PostgreSQL, audio, encrypted secrets, checksums and a
  manifest. Build commit/time appear in health and UI diagnostics.

## Recommended roadmap after v1

1. Add per-system policy objects (default transcription language, retention,
   display name and notification defaults) instead of relying only on global
   settings and per-talkgroup overrides.
2. Add saved listener views and shareable named filters, with a dedicated
   “dispatch”, “fire”, or “priority” home view per user.
3. Add waveform previews, silence trimming markers, skip-forward/back controls,
   and gapless preloading for high-volume listening.
4. Add a purpose-built transcript review queue with keyboard shortcuts,
   confidence/word timestamps when providers supply them, bulk approval, and
   inter-reviewer agreement metrics.
5. Add a dataset schema-version importer/validator and optional redaction rules
   for names, IDs or sensitive talkgroups before training exports.
6. Add WebAuthn/passkeys and optional TOTP for administrators, plus a session
   management page showing/revoking devices.
7. Add OpenTelemetry metrics/traces, Prometheus worker/queue/storage metrics,
   alert thresholds, and a unified operations dashboard.
8. Add object-storage support with signed streaming URLs and lifecycle policies
   while retaining local-disk mode for simple installations.
9. Add PostgreSQL partitioning/archival guidance and indexed transcript search
   for deployments with tens of millions of calls.
10. Add native push notifications and optional offline metadata caching while
    keeping protected audio and private call data out of browser caches.
11. Add translated UI strings and per-user timezone/date-format preferences.
12. Add signed release images, an SBOM, dependency/container scanning, database
    migration compatibility tests, and automated upgrade/rollback drills.
