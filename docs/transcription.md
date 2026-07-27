# Transcription

Transcription is disabled by default. Configure the `transcription_config` row and an OpenAI-compatible endpoint through deployment secrets; no provider key is stored in PostgreSQL. Talkgroup aliases can opt in with system-scoped transcription settings. Jobs are durable and unique per call/provider, with retry history. Commands: `call-recorder-admin transcription queue --call ID`, `transcription run`, `transcription retry --job ID`, and `transcription history`.

Generated transcripts are stored separately from received sender text and notes. Audio remains on the Linux filesystem and is never deleted by transcription.
