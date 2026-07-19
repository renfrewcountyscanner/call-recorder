# Backup and restore

Create a verified backup without copying the live PostgreSQL data directory:

```bash
cd /app/call-recorder
deploy/backup.sh /secure/backup-directory
```

The command writes a custom-format `pg_dump`, separate audio archive, manifest, and SHA-256 checksums. Restore requires an explicit confirmation and should first be rehearsed in an isolated environment:

```bash
CONFIRM_RESTORE=YES deploy/restore.sh /secure/backup-directory/call-recorder-TIMESTAMP
```

The active data paths are `runtime/postgres` and `runtime/audio`. Never restore over a live deployment without a current verified backup and maintenance window.

The isolated restore acceptance procedure uses a temporary PostgreSQL container, temporary audio extraction, and a temporary backend on a non-production port. It verifies a restored call-list page plus normal and HTTP range media responses before tearing those resources down.
