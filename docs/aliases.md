# Aliases

Aliases are scoped by system and numeric talkgroup or radio ID. Display precedence is: enabled manual alias, enabled imported alias, enabled received alias, per-call received value, then numeric ID. Incoming received values never replace manual or imported aliases.

Use the Linux admin command for UTF-8 CSV export/import:

```bash
call-recorder-admin aliases talkgroups export > talkgroups.csv
call-recorder-admin aliases talkgroups import --file talkgroups.csv --dry-run
call-recorder-admin aliases radios export > radios.csv
```

CSV headers are `system_id`, the relevant numeric ID, `alias`, `description`, `category`, `enabled`, and `source`. Manual aliases are protected unless `--overwrite-manual` is explicit.
## Precedence

For a system-scoped numeric ID, the display value is chosen in this order: enabled manual alias, enabled imported alias, enabled received alias, alias contained on the individual call, then the numeric ID. Received metadata can update another received value but never overwrites manual or imported data.

## CSV

Talkgroup and radio CSV files use UTF-8 headers: `system_id`, the relevant ID column (`talkgroup_id` or `radio_id`), `alias`, `description`, `category`, `enabled`, and `source`. Import first with `--dry-run`; imported rows do not replace manual rows unless `--overwrite-manual` is explicitly supplied.
