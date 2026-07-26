# Administration

Administration routes are disabled by default. Set `CALL_RECORDER_ADMIN_ENABLED=true` and a strong `CALL_RECORDER_ADMIN_TOKEN` to enable them, and keep the service behind a private LAN, reverse proxy, or authenticated access layer.

Protected routes are `/admin/talkgroups`, `/admin/radios`, and `/admin/retention`. Alias CSV import/export and destructive retention execution remain Linux `call-recorder-admin` CLI operations.

## Web administration

Administration routes are disabled by default. Set `CALL_RECORDER_ADMIN_ENABLED=true` and a strong private `CALL_RECORDER_ADMIN_TOKEN` only when the service is behind a private LAN, reverse proxy, or other authenticated boundary. Operators can either send the `X-Call-Recorder-Admin` header or visit `/admin/login` to create a one-hour, HttpOnly, SameSite administrative session cookie. The token is never placed in a URL.

Talkgroup and radio pages provide search, call counts, last-seen times, and system-scoped add/update forms. Retention pages create disabled, dry-run policies by default and permit only dry-run previews. Destructive retention remains a Linux CLI action.

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
