# Trunk Recorder Ingestion

The application accepts completed calls only. An adapter must parse a documented, versioned source payload, validate identifiers and timestamps, and register a durable pending call before accepting media.

For observed remote-recorder compatibility, support an ordered two-step exchange:

1. JSON metadata request with sender identity, shared credential, declared `mp3` or `wav` format, and call metadata.
2. Return an opaque upload token after duplicate checking.
3. Accept raw audio for that token and atomically finalize the call.

Preserve the original start time. Retain sender identity without overwriting original receiver identity. Expire incomplete uploads safely and report machine-readable errors. Use synthetic payload fixtures only in this public repository.
