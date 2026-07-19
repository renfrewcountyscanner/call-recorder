# PostgreSQL Data Model

Core tables should include:

- `calls`: local ID, sender ID, receiver ID, timestamps, duration, identifiers, audio metadata/path/hash/size, text, and lifecycle state.
- `call_targets`: one-to-many patched targets.
- `systems`, `sites`, `talkgroups`, and `radio_users`: aliases keyed by system and external identifier.
- `remote_senders`: sender identity, credential reference, enablement, allowed scope, and duplicate policy.
- `pending_uploads`: durable metadata-to-media association with expiry and token hash.
- `ingestion_idempotency`: sender/request/content identity and resolution.
- `retention_policies` and `retention_jobs`: global/per-group rules and auditable work.

Use PostgreSQL foreign keys, unique constraints, indexes for time/filter searches, and full-text search for transcript/notes. Store secrets outside these tables wherever possible; otherwise use references to a protected secret store.
