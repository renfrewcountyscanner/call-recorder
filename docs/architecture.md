# Initial Architecture

## Components

- **Backend:** authenticated HTTP API, remote ingestion state machine, call search, media authorization, retention worker, and WebSocket/polling updates.
- **PostgreSQL:** authoritative metadata, aliases, sender policy, durable pending-upload state, idempotency records, retention policy, and jobs.
- **Media store:** filesystem or object storage, addressed by validated relative paths and media hashes.
- **Frontend:** call table, filters, playback controls, continuous playback, alias administration, and operational status.
- **Uploader:** optional future sender/remote-ingestion component.

## Boundary

The backend commits a call only after validated metadata and media are associated. Pending uploads and retry state must survive restart. Media storage and database deletion are reconciled through auditable retention jobs.

## Security baseline

Use TLS outside a trusted test network, per-sender credentials, viewer/admin roles, bounded upload size/rate, and secret injection through environment or a secret manager. Never commit secret values.
