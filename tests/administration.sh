#!/bin/sh
# Web administration smoke coverage; only callrecorder_it/.test-runtime are used.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="docker-compose --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
work=$(mktemp -d)
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
trap cleanup EXIT
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio"
CALL_RECORDER_ADMIN_ENABLED=true CALL_RECORDER_ADMIN_TOKEN=synthetic-admin-token $compose up -d --build
for n in $(seq 1 40); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
curl -fsS -c "$work/cookie" -d 'token=synthetic-admin-token' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/login | grep -q 303
curl -fsS -b "$work/cookie" -d 'system=system-z&id=900&alias=Manual+Dispatch&description=synthetic&category=test&priority=4&source=manual&enabled=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/talkgroups | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select alias from talkgroup_aliases where system_id='system-z' and talkgroup_id='900'")" = 'Manual Dispatch'
curl -fsS -b "$work/cookie" -d 'system=system-z&id=901&alias=Manual+Unit&description=synthetic&category=test&source=manual&enabled=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/radios | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select alias from radio_aliases where system_id='system-z' and radio_id='901'")" = 'Manual Unit'
curl -fsS -b "$work/cookie" -d 'name=synthetic-policy&retention_days=30&priority=1&dry_run=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/retention | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select count(*) from retention_policies where name='synthetic-policy' and enabled=false and dry_run=true")" = 1
policy_id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select id from retention_policies where name='synthetic-policy'")
curl -fsS -b "$work/cookie" -d "id=$policy_id&name=synthetic-policy-updated&retention_days=31&priority=2&dry_run=on" -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/retention | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select retention_days from retention_policies where id=$policy_id")" = 31
curl -fsS -b "$work/cookie" http://127.0.0.1:18080/admin/retention | grep -q 'synthetic-policy'
curl -fsS -b "$work/cookie" http://127.0.0.1:18080/admin/favourites | grep -q 'Favourite groups'
curl -fsS -b "$work/cookie" http://127.0.0.1:18080/admin/notifications | grep -q 'Notifications'
curl -fsS -b "$work/cookie" http://127.0.0.1:18080/admin/transcription | grep -q 'Transcription'
curl -fsS -b "$work/cookie" -d 'name=synthetic-favourites&description=web-test&display_order=1&enabled=on' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/favourites | grep -q 303
group_id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select id from favourite_groups where name='synthetic-favourites'")
curl -fsS -b "$work/cookie" -d "group_id=$group_id&system_id=system-z&talkgroup_id=900" -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/favourites/member | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select count(*) from favourite_members where group_id=$group_id")" = 1
curl -fsS -b "$work/cookie" -d 'name=synthetic-destination&type=webhook&url=https%3A%2F%2Fexample.invalid%2Fhook&secret_ref=SYNTHETIC_SECRET' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/notifications/destination | grep -q 303
dest_id=$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select id from notification_destinations where name='synthetic-destination'")
curl -fsS -b "$work/cookie" -d "name=synthetic-rule&destination_id=$dest_id&priority=2&system=system-z&frequency_min=800&frequency_max=900&min_duration_ms=100&max_duration_ms=5000&patched_only=on&favourite_group_id=$group_id&keyword=test" -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/notifications/rule | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select patched_only from notification_rules where name='synthetic-rule'")" = t
curl -fsS -b "$work/cookie" http://127.0.0.1:18080/admin/notifications/history | grep -q 'Notification delivery history'
sender_page=$(curl -fsS -b "$work/cookie" -d 'sender_id=web-test-sender' http://127.0.0.1:18080/admin/senders/create)
printf '%s' "$sender_page" | grep -q 'New API key for web-test-sender'
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select enabled from remote_senders where sender_id='web-test-sender'")" = t
# key_hash is a bytea column; verify a non-empty Argon2id encoding without
# relying on PostgreSQL's bytea display representation.
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select octet_length(key_hash)>32 from remote_senders where sender_id='web-test-sender'")" = t
curl -fsS -b "$work/cookie" -d 'sender_id=web-test-sender' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/senders/disable | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select enabled from remote_senders where sender_id='web-test-sender'")" = f
echo 'administration tests passed'
