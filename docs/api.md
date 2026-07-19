# API and local startup

## Two-stage ingestion

`POST /api/v1/uploads` accepts JSON with `sender_id`, optional `idempotency_key`, `audio_format` (`mp3` or `wav`), and a `call` object. Send the per-sender key in `X-Call-Recorder-Key`. A successful non-duplicate response returns an opaque `upload_token` and expiry.

`POST /api/v1/uploads/{upload_token}` accepts the matching raw `audio/mpeg` or WAV content and returns a completed call ID. It repeats both `X-Call-Recorder-Sender` and `X-Call-Recorder-Key`; they must match the authenticated sender that created the pending upload. Tokens are stored only as hashes in PostgreSQL and expire. The browser uses `GET /`, `GET /calls?q=&sender=&system=&talkgroup=&radio=&date=`, and `GET /media/{call-id}`. Media supports HTTP byte ranges.

## Start with Docker Compose

```bash
cd deploy
cp example.env .env
# Replace every CHANGE_ME value with strong private values.
docker-compose up --build
```

Open `http://localhost:8080`. Docker named volumes retain PostgreSQL metadata and audio across restarts.

## Uploader

The uploader is a Go command. It reads a synthetic metadata JSON file and a completed MP3/WAV file, posts metadata, then posts audio after receiving the token. Do not use real recordings or credentials in repository fixtures.

## Sender administration

Provision senders through the local administrative command, not a browser endpoint. It generates a 256-bit API key, stores only an Argon2id hash, and prints the plaintext value once:

```bash
cd deploy
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend sender create --name integration-test-recorder
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend sender replace --name integration-test-recorder
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend sender disable --name integration-test-recorder
```
