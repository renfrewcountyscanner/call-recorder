# Phase 8 workers

The transcription worker is part of the normal Docker Compose stack. It starts automatically, remains idle when transcription is disabled, and reloads its PostgreSQL-managed configuration without rebuilding. It runs unprivileged, exposes no public port, mounts audio read-only and secrets read-only, and never requires the Docker socket.

The notification worker is part of the normal Compose stack. It wakes every 15 seconds and expires queued alerts older than the configured safety window before delivery.

Workers use PostgreSQL job claiming with `FOR UPDATE SKIP LOCKED`, advisory locks, bounded retries, request timeouts, response size limits, and restart policies. Missing transcription configuration or secrets produce a clear idle or diagnostic message instead of failing the backend.
