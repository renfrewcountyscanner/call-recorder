# Administration

Administration routes are disabled by default. Set `CALL_RECORDER_ADMIN_ENABLED=true` and a strong `CALL_RECORDER_ADMIN_TOKEN` to enable them, and keep the service behind a private LAN, reverse proxy, or authenticated access layer.

Protected routes are `/admin/talkgroups`, `/admin/radios`, `/admin/retention`, and `/admin/retention/history`. Alias CSV import/export and destructive retention execution remain Linux `call-recorder-admin` CLI operations.

## Web administration

Administration routes are disabled by default. Set `CALL_RECORDER_ADMIN_ENABLED=true` and a strong private `CALL_RECORDER_ADMIN_TOKEN` only when the service is behind a private LAN, reverse proxy, or other authenticated boundary. Operators can either send the `X-Call-Recorder-Admin` header or visit `/admin/login` to create a one-hour, HttpOnly, SameSite administrative session cookie. The token is never placed in a URL.

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

For production, put the service behind a Cloudflare Access application configured for Google authentication and require login for the site. Set `CALL_RECORDER_CLOUDFLARE_ACCESS_ENABLED=true` and `CALL_RECORDER_CLOUDFLARE_ADMIN_EMAIL=renfrewcountyscanner@gmail.com`. Cloudflare supplies the authenticated identity in `Cf-Access-Authenticated-User-Email`; only that exact configured email receives administrative rights. Other authenticated users remain guests: they can browse and play calls but cannot change aliases, manage sender credentials, or run retention operations.

When Cloudflare mode is enabled, the local `CALL_RECORDER_ADMIN_TOKEN` login is bypassed. Do not expose the origin directly; otherwise clients could forge the identity header. Restrict origin access to Cloudflare or an equivalent trusted reverse proxy.
