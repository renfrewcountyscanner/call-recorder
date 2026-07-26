# Development and synthetic integration testing

Use Docker Compose from `deploy/`; PostgreSQL is supported only as the Compose service. Create a private `.env` from `example.env`, then run `docker-compose config -q`, `docker-compose build`, and `docker-compose up -d`.

Provision a sender with `docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend sender create --name NAME`. Store the printed key in a private password manager or a mode-restricted temporary file; it is not recoverable from the database.

Build the uploader with `docker run --rm -v "$PWD":/src -w /src/uploader golang:1.26-alpine go test ./...`. Use only synthetic JSON and WAV/MP3 fixtures outside the repository. The uploader posts metadata, receives an opaque token, then uploads audio with both sender-authentication headers.

Run the isolated PostgreSQL-backed server smoke suite without touching `runtime/` or the live Compose project:

```bash
cd /app/call-recorder
tests/integration.sh
tests/retention.sh
tests/administration.sh
tests/phase6.sh
```

It starts a separate Compose project on port 18080, uses `.test-runtime`, sends synthetic WAV metadata/audio, verifies duplicate prevention and a `206` range response, then removes the test project and temporary runtime state.

`tests/retention.sh` first runs the same isolated ingestion suite, then executes a destructive synthetic retention policy against only `.test-runtime`. It never accesses `runtime/`.

`tests/administration.sh` verifies the protected login/session flow, alias update, and safe default retention-policy creation. `tests/phase6.sh` runs the complete isolated Phase 6 group, including existing Chromium sequential playback coverage.

Apply additive migrations after making a verified backup:

```bash
deploy/migrate.sh
```

The command is repeatable: existing objects are retained. It must be run from the deployment host with the production Compose configuration available.

Run the Chromium sequential-playback acceptance test against the same isolated deployment:

```bash
tests/browser-sequential.sh
```

It injects a controlled media `play()` stub in Chromium, dispatches actual `ended` events, and verifies that the page’s playback handler advances through synthetic calls. The production duplicate resend is deferred to the real Trunk Recorder host; see `docs/production-duplicate-test.md`.
