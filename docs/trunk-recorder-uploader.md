# Trunk Recorder uploader

The Linux Go uploader sends completed-call metadata first, then the corresponding MP3 or WAV only after receiving an opaque token. Each remote recorder has its own sender name and API key. Senders are authenticated for both stages. Retry the same completed call with the same sender and metadata; duplicate detection is scoped to that sender, so independently operated senders do not collide.
