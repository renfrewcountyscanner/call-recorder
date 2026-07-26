# Phase 8 candidates

These items were observed as capabilities or extension points in the legacy interoperability review but are intentionally deferred from the v0.3.0 WebUI completion. None are required for completed-call ingestion, browsing, playback, aliases, retention, or administration.

| Candidate | Confirmed legacy behavior | Current Linux status | User value | Backend/schema/security needs | Priority |
|---|---|---|---|---|---|
| Streaming output and stream mounts | Cannot verify as a required completed-call upload behavior | Not implemented | Live monitoring | Stream lifecycle, authorization, resource limits | Medium |
| Streaming talkgroup membership | Cannot verify from the clean-room evidence | Not implemented | Focused live monitoring | Membership model and update API | Medium |
| Streaming queue lockout management | Cannot verify | Not implemented | Suppress unwanted live audio | Admin authorization and audit history | Low |
| SMTP notifications | Cannot verify | Not implemented | Operational alerts | Recipient secrets, rate limits, delivery failures | Low |
| Speech transcription and subtitles | Transcript fields are observable; processing service is not confirmed | Storage fields exist; processing not implemented | Searchable text and accessibility | Worker queue, provider security, retention | Medium |
| Favourite talkgroup groups | Cannot verify | Not implemented | Personal workflow shortcuts | User identity model | Low |
| Full users and roles | Current deployment uses Cloudflare Access plus admin authorization | Guest/admin policy is external; full roles not implemented | Multi-user administration | User/session/role schema and CSRF/audit controls | Medium |
| UDP listener | Cannot verify | Not implemented | Alternate integration | Listener isolation and protocol definition | Low |
| Additional media servers | Cannot verify | Not implemented | External playback federation | Proxy authorization and path safety | Low |
| ACME certificate automation | Not part of the Linux application | Not implemented | Easier private deployment | Certificate lifecycle and renewal | Low |
| Windows-specific importers | Windows-only scope | Deliberately excluded | None for Linux deployment | No Linux requirement | None |
| SQLite or SQL Server support | Legacy storage options are not Linux deployment requirements | Deliberately excluded | None for PostgreSQL release | Would fragment schema and operations | None |

