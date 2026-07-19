# Development and synthetic integration testing

Use Docker Compose from `deploy/`; PostgreSQL is supported only as the Compose service. Create a private `.env` from `example.env`, then run `docker-compose config -q`, `docker-compose build`, and `docker-compose up -d`.

Provision a sender with `docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend sender create --name NAME`. Store the printed key in a private password manager or a mode-restricted temporary file; it is not recoverable from the database.

Build the uploader with `docker run --rm -v "$PWD":/src -w /src/uploader golang:1.26-alpine go test ./...`. Use only synthetic JSON and WAV/MP3 fixtures outside the repository. The uploader posts metadata, receives an opaque token, then uploads audio with both sender-authentication headers.
