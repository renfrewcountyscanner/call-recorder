# Development and synthetic integration testing

Use Docker Compose from `deploy/`; PostgreSQL is supported only as the Compose service. Create a private `.env` from `example.env`, then run `docker-compose config -q`, `docker-compose build`, and `docker-compose up -d`.

Provision a sender with `docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend sender create --name NAME`. Store the printed key in a private password manager or a mode-restricted temporary file; it is not recoverable from the database.

Build the uploader with `docker run --rm -v "$PWD":/src -w /src/uploader golang:1.26-alpine go test ./...`. Use only synthetic JSON and WAV/MP3 fixtures outside the repository. The uploader posts metadata, receives an opaque token, then uploads audio with both sender-authentication headers.

Run the isolated PostgreSQL-backed server smoke suite without touching `runtime/` or the live Compose project:

```bash
cd /app/call-recorder
tests/integration.sh
```

It starts a separate Compose project on port 18080, uses `.test-runtime`, sends synthetic WAV metadata/audio, verifies duplicate prevention and a `206` range response, then removes the test project and temporary runtime state.

Run the Chromium sequential-playback acceptance test against the same isolated deployment:

```bash
tests/browser-sequential.sh
```

It injects a controlled media `play()` stub in Chromium, dispatches actual `ended` events, and verifies that the page’s playback handler advances through synthetic calls. The production duplicate resend is deferred to the real Trunk Recorder host; see `docs/production-duplicate-test.md`.
