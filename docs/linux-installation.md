# Linux installation

Install Docker Engine and the legacy `docker-compose` command supplied by the host distribution. Clone this repository, create `deploy/.env` with unique local secrets, and run `docker-compose up -d --build` from `deploy/`. PostgreSQL metadata and Linux filesystem audio use named Docker volumes; do not use a host PostgreSQL service.

Use `docker-compose down` and `docker-compose up -d` for normal restart tests. Do not use `down -v` in normal operation because it removes persisted metadata and audio volumes.
