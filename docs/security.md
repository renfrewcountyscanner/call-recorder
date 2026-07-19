# Security

The backend runs as an unprivileged container user. A one-shot Compose service prepares the audio volume with mode 0750 and the backend user ownership. API keys are randomly generated and saved only as Argon2id hashes; upload tokens are stored only as SHA-256 hashes. Sender filenames never select permanent storage paths, temporary uploads remain inside the configured storage root, and audio request bodies are size-limited. Do not commit `.env`, database volumes, recordings, logs, API keys, or tokens.
