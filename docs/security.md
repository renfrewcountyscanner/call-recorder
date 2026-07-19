# Security

The backend runs as an unprivileged container user. A one-shot Compose service prepares `/app/call-recorder/runtime/audio` with mode 0750 and backend ownership; PostgreSQL uses `/app/call-recorder/runtime/postgres` with mode 0700. API keys are randomly generated and saved only as Argon2id hashes; upload tokens are stored only as SHA-256 hashes. Sender filenames never select permanent storage paths, temporary uploads remain inside the configured storage root, and audio request bodies are size-limited. Do not commit `.env`, runtime data, recordings, logs, API keys, or tokens.
