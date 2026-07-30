#!/bin/sh
# Cloudflare Access identity mapping smoke test; isolated synthetic data only.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose="${COMPOSE:-docker compose} --project-name callrecorder_it --env-file $root/deploy/integration.env -f $root/deploy/docker-compose.yml -f $root/deploy/docker-compose.integration.yml"
cleanup() { $compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$root/.test-runtime"; }
trap cleanup EXIT
KEEP_TEST_ENV=1 "$root/tests/integration.sh"
CALL_RECORDER_ADMIN_ENABLED=true CALL_RECORDER_ADMIN_TOKEN= CALL_RECORDER_CLOUDFLARE_ACCESS_ENABLED=true CALL_RECORDER_CLOUDFLARE_ADMIN_EMAIL=renfrewcountyscanner@gmail.com CALL_RECORDER_CLOUDFLARE_TRUSTED_PROXY_IPS=172.30.0.1 $compose up -d --no-deps --force-recreate backend >/dev/null
for n in $(seq 1 40); do curl -fsS http://127.0.0.1:18080/healthz >/dev/null && break; sleep 1; done
test "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/admin/senders)" = 401
test "$(curl -s -o /dev/null -w '%{http_code}' -H 'Cf-Access-Authenticated-User-Email: guest@example.com' http://127.0.0.1:18080/admin/senders)" = 401
test "$(curl -s -o /dev/null -w '%{http_code}' -H 'Cf-Access-Authenticated-User-Email: renfrewcountyscanner@gmail.com' http://127.0.0.1:18080/admin/senders)" = 200
echo 'Cloudflare Access admin mapping passed'
