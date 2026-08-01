# Administration

Browser pages can be protected site-wide with `CALL_RECORDER_AUTH_REQUIRED=true` (the production default). Set `CALL_RECORDER_ADMIN_ENABLED=true` and a strong `CALL_RECORDER_SESSION_SECRET`; keep the service behind Cloudflare Access or a private reverse proxy.

Protected routes are `/admin/talkgroups`, `/admin/radios`, `/admin/retention`, and `/admin/retention/history`. Alias CSV import/export and destructive retention execution remain Linux `call-recorder-admin` CLI operations.

## Web administration

When `CALL_RECORDER_AUTH_REQUIRED=true`, unauthenticated browser requests are redirected to `/login`; calls and audio are not rendered before sign-in. Local username/password login creates a one-hour HttpOnly, SameSite session. The legacy `X-Call-Recorder-Admin` header is for private maintenance compatibility only.

Talkgroup and radio pages provide search (preserved across edits), call counts, last-seen times, source badges (manual, imported, received, numeric fallback), and in-page edit forms. Retention pages create disabled, dry-run policies by default, show obvious enabled and dry-run/live-delete badges, and permit only dry-run previews; policy deletion asks for explicit confirmation. `/admin/retention/history` lists every recorded execution. Destructive retention remains a Linux CLI action.

## CLI

Use the Compose backend image without exposing credentials in shell history:

```bash
cd deploy
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend retention list
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend retention run --dry-run
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend retention run --policy 3
docker-compose run --rm --entrypoint /usr/local/bin/call-recorder-admin backend retention history
```

The last command that omits `--dry-run` can delete calls only when an enabled policy is configured `dry_run=false`; review a dry run first.

## Cloudflare Access and Google authentication

For production, put the service behind a Cloudflare Access application configured for Google authentication and require login for the site. Set `CALL_RECORDER_CLOUDFLARE_ACCESS_ENABLED=true`, `CALL_RECORDER_CLOUDFLARE_ADMIN_EMAIL=renfrewcountyscanner@gmail.com`, `CALL_RECORDER_CLOUDFLARE_TRUSTED_PROXY_IPS` to the tunnel/reverse-proxy source addresses, and `CALL_RECORDER_AUTH_LOGIN_URL` to the Access login URL. Cloudflare supplies the authenticated identity in `Cf-Access-Authenticated-User-Email`; only that exact configured email receives administrative rights. Other authenticated users become read-only viewers. Identity headers from any other source are rejected.

The local fallback is controlled by `CALL_RECORDER_LOCAL_AUTH_ENABLED`. Use `CALL_RECORDER_SESSION_COOKIE_SECURE=true` behind HTTPS; set it to false only for private HTTP development. Login attempts are rate-limited and all administrative writes require an administrator role.

When Cloudflare mode is enabled, trusted proxy requests use the Cloudflare identity; local login remains available only when `CALL_RECORDER_LOCAL_AUTH_ENABLED=true` for private-LAN fallback. Do not expose the origin directly; otherwise clients could forge the identity header. Restrict origin access to Cloudflare or an equivalent trusted reverse proxy.
Phase 8 adds favourite groups, protected-call actions, notification destination metadata, and transcription queue status. All writes remain behind the existing admin token/Cloudflare Access checks. Secret values are never entered into or displayed by the application; use deployment environment or Docker secret references.

## Storage capacity

Administrators can open **Administration → Storage** or the storage card on **Receiver status**. The gauge reads the current filesystem containing `CALL_RECORDER_AUDIO_ROOT` and shows total, used, free, and free percentage. It is a current-capacity gauge, not a historical chart. Absolute filesystem paths and secrets are never displayed.
