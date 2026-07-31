#!/bin/sh
# Web administration smoke coverage; only callrecorder_it/.test-runtime are used.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="${COMPOSE:-docker compose} --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
work=$(mktemp -d)
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime" "$work"; }
trap cleanup EXIT
mkdir -p "$root/.test-runtime/postgres" "$root/.test-runtime/audio"
CALL_RECORDER_ADMIN_ENABLED=true CALL_RECORDER_SESSION_SECRET=test-secret-key-1234567890 $compose up -d --build
for n in $(seq 1 40); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1 && break || true; sleep 1; done
# Create admin user via CLI
$compose exec -T backend /usr/local/bin/call-recorder-admin users create --username admin --password testpassword --role admin || true
# Login with username/password
curl -fsS -c "$work/cookie" -d 'username=admin&password=testpassword' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/login | grep -q 303
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
# Enable transcription for a talkgroup and confirm it is listed.
curl -fsS -b "$work/cookie" -d 'system=system-z&id=900&alias=Manual+Dispatch&description=synthetic&category=test&priority=4&source=manual&enabled=on&transcription_enabled=on&transcription_language=en' -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/talkgroups | grep -q 303
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select transcription_enabled||','||transcription_language from talkgroup_aliases where system_id='system-z' and talkgroup_id='900'")" = 'true,en'
secret_page=$(curl -fsSL -b "$work/cookie" -d 'api_key=synthetic-transcription-key' http://127.0.0.1:18080/admin/transcription/secret)
printf '%s' "$secret_page" | grep -q 'API key configured'
if printf '%s' "$secret_page" | grep -q 'synthetic-transcription-key'; then echo 'secret disclosure'; exit 1; fi
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select octet_length(ciphertext)>0 and octet_length(nonce)>0 from application_secrets where purpose='transcription_api_key'")" = t
curl -fsSL -b "$work/cookie" -d 'api_key=synthetic-transcription-key-2' http://127.0.0.1:18080/admin/transcription/secret >/dev/null
curl -fsSL -b "$work/cookie" -d '' http://127.0.0.1:18080/admin/transcription/secret/remove >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select count(*) from application_secrets where purpose='transcription_api_key'")" = 0
curl -fsSL -b "$work/cookie" -d 'enabled=on&processing_enabled=on&provider=openai-compatible&provider_type=faster-whisper&endpoint=http%3A%2F%2Ffake-transcriber%3A9912%2Fv1%2Faudio%2Ftranscriptions&model=whisper-v3&language=en&min_duration_seconds=0.5&max_duration_minutes=15&max_file_size_mb=50&temperature=0&vad_enabled=on&request_timeout_seconds=60&concurrency=1&retry_limit=3&allowed_endpoint_cidrs=192.168.2.2%2F32' http://127.0.0.1:18080/admin/transcription/config >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select provider_type||','||min_duration_ms||','||max_audio_duration_ms||','||max_file_size||','||processing_enabled from transcription_config where id=true")" = 'faster-whisper,500,900000,52428800,true'
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
# Transcription: provider type and CIDR validation.
curl -fsS -b "$work/cookie" -d 'enabled=on&processing_enabled=on&provider=openai&provider_type=openai-compatible&endpoint=http%3A%2F%2Fexample.invalid%2Fv1%2Faudio%2Ftranscriptions&model=whisper-v3&language=en&min_duration_seconds=0.5&max_duration_minutes=15&max_file_size_mb=50&temperature=0&request_timeout_seconds=60&concurrency=1&retry_limit=3' http://127.0.0.1:18080/admin/transcription/config >/dev/null
test "$($compose exec -T postgres psql -U call_recorder_test -d call_recorder_test -Atc "select provider_type from transcription_config where id=true")" = 'openai-compatible'
# Invalid CIDR should be rejected.
if curl -fsS -b "$work/cookie" -d 'enabled=on&processing_enabled=on&provider=openai&provider_type=openai-compatible&endpoint=http%3A%2F%2Fexample.invalid%2Fv1%2Faudio%2Ftranscriptions&model=whisper-v3&language=en&min_duration_seconds=0.5&max_duration_minutes=15&max_file_size_mb=50&temperature=0&request_timeout_seconds=60&concurrency=1&retry_limit=3&allowed_endpoint_cidrs=not-a-cidr' http://127.0.0.1:18080/admin/transcription/config >/dev/null 2>&1; then echo 'invalid CIDR accepted'; exit 1; fi
echo 'administration tests passed'
