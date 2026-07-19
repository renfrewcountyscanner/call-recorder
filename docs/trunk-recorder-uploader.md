# Trunk Recorder uploader

The Linux Go uploader sends completed-call metadata first, then the corresponding MP3 or WAV only after receiving an opaque token. Each remote recorder has its own sender name and API key. Senders are authenticated for both stages. Retry the same completed call with the same sender and metadata; duplicate detection is scoped to that sender, so independently operated senders do not collide.

For direct Linux Trunk Recorder installations, use the lightweight durable sender under `uploader/trunk-recorder/`. It has an external mode-600 configuration file, filesystem spool, bounded retry, optional secondary destination, and systemd drain timer.
