# Aliases

Aliases are scoped by system and numeric talkgroup or radio ID. Display precedence is: enabled manual alias, enabled imported alias, enabled received alias, per-call received value, then numeric ID. Incoming received values never replace manual or imported aliases.

Use the Linux admin command for UTF-8 CSV export/import:

```bash
call-recorder-admin aliases talkgroups export > talkgroups.csv
call-recorder-admin aliases talkgroups import --file talkgroups.csv --dry-run
call-recorder-admin aliases radios export > radios.csv
```

CSV headers are `system_id`, the relevant numeric ID, `alias`, `description`, `category`, `enabled`, and `source`. Manual aliases are protected unless `--overwrite-manual` is explicit.
