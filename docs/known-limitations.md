# Known limitations and follow-up work

v0.2.0 adds alias administration and safe-by-default retention to the working v0.1.0 release. The following are planned maintenance work, not release blockers:

- Administration session cookies are derived from the administration token and expire only in the browser; rotate the administration token to revoke active sessions.
- The administration login has no rate limiting. Keep the service behind a private LAN, reverse proxy, or other authenticated access layer.
- Alias CSV import is not transactional; a mid-file failure can leave a partial import that is safe to re-run.
- A retention run interrupted by a process kill can leave staged audio under `.retention-trash` inside the audio root. Call rows remain in PostgreSQL; staged files can be moved back manually.
- Expand authentication and validation integration coverage.
- Expand automated sender interruption, restart, spool-recovery, and secondary-destination testing.
- Run the deferred production duplicate resend on the actual Trunk Recorder host.
- Rotate operational credentials through an operational change process.

No Windows compatibility claim is made by this release.
