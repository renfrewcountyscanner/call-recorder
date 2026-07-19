# Administration

Administration routes are disabled by default. To expose read-only administration listings, set `CALL_RECORDER_ADMIN_ENABLED=true` and a strong `CALL_RECORDER_ADMIN_TOKEN`, then send that value only in the `X-Call-Recorder-Admin` request header. Keep the service behind a private LAN, reverse proxy, or authenticated access layer.

Available read-only routes are `/admin/talkgroups`, `/admin/radios`, and `/admin/retention`. Use the Linux `call-recorder-admin` command for write operations, CSV import/export, and retention execution until a dedicated authenticated write UI is deployed.
