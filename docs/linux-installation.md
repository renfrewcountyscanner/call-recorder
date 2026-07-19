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
