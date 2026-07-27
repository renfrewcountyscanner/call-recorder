# Linux installation

Install Docker Engine and the legacy `docker-compose` command supplied by the host distribution. Clone this repository, create `deploy/.env` with unique local secrets, and run `docker-compose up -d --build` from `deploy/`. PostgreSQL metadata is persisted at `/app/call-recorder/runtime/postgres` and Linux audio at `/app/call-recorder/runtime/audio`; do not use a host PostgreSQL service.

Use `docker-compose down` and `docker-compose up -d` for normal restart tests. Do not use `down -v` in normal operation because it removes persisted metadata and audio volumes.

Upgrade from v0.1.0 after taking a verified backup:

```bash
cd /app/call-recorder
deploy/backup.sh /secure/backups
deploy/migrate.sh
cd deploy && docker-compose up -d --build
```

## Upgrade from v0.1.0

1. Create and verify a backup: `deploy/backup.sh /safe/backup-directory`.
2. Pull the v0.2.0 release when available.
3. Run `deploy/migrate.sh` from the repository root.
4. Rebuild and restart: `cd deploy && docker-compose up -d --build`.
5. Leave all retention policies disabled until their dry-run result has been reviewed.

No migration stores audio in PostgreSQL or enables deletion automatically.

## Upgrade from v0.2.0 to v0.3.0

Perform this upgrade during a short maintenance window. It is additive and does not remove calls or audio.

1. Create and verify a fresh backup (database checksum and audio manifest):
   `deploy/backup.sh /safe/backup-directory`.
2. Pull and check out the v0.3.0 release.
3. Apply migration 004 explicitly: `deploy/migrate.sh` (it is safe to rerun; already-applied migrations are skipped).
4. Build and start the v0.3.0 image: `cd deploy && docker-compose build backend && docker-compose up -d`.
5. Verify `docker-compose ps`, `/healthz`, `/readyz`, one synthetic or normal ingestion, browser call listing, and byte-range playback.

Rollback: stop the backend, keep the PostgreSQL/audio runtime directories intact, restore the verified backup with `CONFIRM_RESTORE=YES deploy/restore.sh`, then start the previously known-good image/commit. Do not use `docker-compose down -v`.

## Upgrade from v0.3.0 to v0.4.0

1. Take and verify a fresh backup: `deploy/backup.sh /safe/backup-directory`.
2. Check out the v0.4.0 release and run `deploy/migrate.sh` (migration 005 is additive and repeatable).
3. Build and start the backend: `cd deploy && docker-compose build backend && docker-compose up -d`.
4. Verify `/healthz`, `/readyz`, call ingestion, search, and playback. Leave notification and transcription workers disabled until their secret references and test endpoints are configured.

Optional workers, after configuration and an isolated test, can be enabled independently:

```bash
cd /app/call-recorder/deploy
docker-compose --profile notifications up -d notification-worker
docker-compose --profile transcription up -d transcription-worker
# or enable both profiles together
docker-compose --profile notifications --profile transcription up -d
```

Notification destinations use secret reference names; transcription uses the configured endpoint, model, and secret reference. Do not put secret values in Git or PostgreSQL.

Rollback by stopping the backend, restoring the verified backup with `CONFIRM_RESTORE=YES deploy/restore.sh`, and restarting the v0.3.0 image. Never use `down -v`.
