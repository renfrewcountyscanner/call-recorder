# Retention

Retention policies are disabled and dry-run by default. Policies run in priority order under a PostgreSQL advisory lock. A dry-run records match counts only. A destructive run stages files in an in-root `.retention-trash` directory, commits database deletion, then removes staged audio; a database failure moves staged files back.

Use the Linux admin command:

```bash
call-recorder-admin retention list
call-recorder-admin retention run --dry-run
call-recorder-admin retention history
```

Run and review dry-run output before enabling a policy. Keep administration behind private/authenticated access.
## Safety model

Policies are disabled and dry-run by default. The runner takes a PostgreSQL advisory lock, selects only eligible old calls, verifies each relative path stays inside the configured audio root, moves audio to an audio-root-local trash area, commits database deletion, and only then removes moved files. A transaction failure moves audio back. Every execution is recorded in `retention_runs`.

Use `tests/retention.sh` for destructive coverage; it uses only `callrecorder_it` and `.test-runtime`. Never use a live runtime directory for retention testing.
