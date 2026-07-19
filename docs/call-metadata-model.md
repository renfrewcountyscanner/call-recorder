# Call Metadata Model

Each call needs a local ID, sender/source ID, receiver ID, original UTC start time, duration, audio format, relative audio path, system/site IDs and aliases, target/talkgroup IDs and aliases/tags, radio/source IDs and aliases/tags, frequency, LCN, call type, group/private classification, optional patched targets, audio-start offset, and optional transcript/notes.

End time is derived from start time and duration unless an authoritative source provides it. The model reserves optional fields for encryption, emergency state, media size, media hash, and ingestion state; it must not invent values absent from source data.

Alias entities are keyed by system plus identifier. Patched targets are a child relation of a call. Preserve raw inbound metadata under access controls for troubleshooting while retaining a normalized public model.
