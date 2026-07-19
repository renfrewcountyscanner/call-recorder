# Retention

Retention policies are disabled and dry-run by default. Policies run in priority order under a PostgreSQL advisory lock. A dry-run records match counts only. A destructive run stages files in an in-root `.retention-trash` directory, commits database deletion, then removes staged audio; a database failure moves staged files back.

Use the Linux admin command:

```bash
call-recorder-admin retention list
call-recorder-admin retention run --dry-run
call-recorder-admin retention history
```

Run and review dry-run output before enabling a policy. Keep administration behind private/authenticated access.
