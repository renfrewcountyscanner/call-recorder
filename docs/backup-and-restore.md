# Backup and restore

Create a verified backup without copying the live PostgreSQL data directory:

```bash
cd /app/call-recorder
deploy/backup.sh /secure/backup-directory
```

The command writes a custom-format `pg_dump`, separate audio archive, encrypted-secrets archive when `runtime/secrets` exists, manifest, and SHA-256 checksums. The secrets archive contains the master key and must be protected like the database backup. Restore requires an explicit confirmation and should first be rehearsed in an isolated environment:

```bash
CONFIRM_RESTORE=YES deploy/restore.sh /secure/backup-directory/call-recorder-TIMESTAMP
```

The active data paths are `runtime/postgres`, `runtime/audio` and `runtime/secrets`. Never restore a database containing encrypted application secrets without restoring the matching `runtime/secrets/master.key`; otherwise encrypted keys are intentionally undecryptable. Never restore over a live deployment without a current verified backup and maintenance window.

The isolated restore acceptance procedure uses a temporary PostgreSQL container, temporary audio extraction, and a temporary backend on a non-production port. It verifies a restored call-list page plus normal and HTTP range media responses before tearing those resources down.
Migration 005 is additive. Back up before applying it, verify the manifest/checksums, and leave optional notification/transcription workers disabled during the upgrade. Roll back by restoring the verified pre-upgrade backup and the prior backend image.
