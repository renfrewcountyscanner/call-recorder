# Phase 8 workers

Notification and transcription workers are optional Compose profiles. Enable notifications with `docker-compose --profile notifications up -d notification-worker`; enable transcription only when a compatible endpoint is configured with `--profile transcription`. Workers are unprivileged, have no public ports, use PostgreSQL job claiming/advisory locks, bounded retries, request timeouts, response limits, and restart policies. The default stack does not require either worker. Missing optional secret references fail the selected worker clearly without affecting the backend.
