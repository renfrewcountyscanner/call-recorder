# Transcription

Transcription is disabled by default. Administrators configure the provider from `/admin/transcription`; endpoint, model, language, human-readable duration/size limits, processing controls, CIDR allowlist and worker settings are stored in PostgreSQL. The API key is entered once through the protected UI and encrypted with AES-256-GCM under `runtime/secrets/master.key`; the plaintext key is never stored in PostgreSQL or returned to the browser. The always-present Compose worker remains idle until both provider and processing are enabled. Talkgroup aliases opt in with system-scoped settings. Jobs are durable and unique per call/provider, with retry history. Commands remain available: `call-recorder-admin transcription queue --call ID`, `transcription run`, `transcription retry --job ID`, and `transcription history`.

The supported LAN endpoint example is `http://192.168.2.2:9912/v1/audio/transcriptions` with an explicit `192.168.2.2/32` endpoint allowlist. Private, loopback, link-local and metadata-service destinations remain blocked unless explicitly permitted by the configured policy. Use the UI's provider test with a synthetic WAV only.

Generated transcripts are stored separately from received sender text and notes. Audio remains on the Linux filesystem and is never deleted by transcription.
