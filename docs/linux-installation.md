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
