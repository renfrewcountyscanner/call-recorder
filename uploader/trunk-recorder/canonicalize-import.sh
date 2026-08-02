#!/usr/bin/env bash
# Convert calls uploaded through legacy-import to the logger's canonical IDs.
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [[ -z "${CALL_RECORDER_DATABASE_URL:-}" && -r "$script_dir/../../deploy/.env" ]]; then
    set -a
    . "$script_dir/../../deploy/.env"
    set +a
fi
if [[ -z "${CALL_RECORDER_DATABASE_URL:-}" ]]; then
    echo "Canonicalization skipped: CALL_RECORDER_DATABASE_URL is unavailable." >&2
    exit 2
fi

priority_sql="UPDATE transcription_jobs j SET priority=-100,updated_at=now()
FROM pending_uploads p
WHERE p.sender_id='legacy-import' AND p.status='completed' AND p.completed_call_id=j.call_id AND j.priority<>-100;"

mapping_sql="WITH mapping(old_receiver,sender,receiver,system_id,system_name) AS (VALUES
 ('Pembroke Twr','SCANNER-DIGITAL','BCD996P2','SCANNER-DIGITAL','Scanner Digital'),
 ('Services','SCANNER-ANALOG','BCT15X','SCANNER-ANALOG','Scanner Analog'),
 ('RDIO-ANALOG','SCANNER-ANALOG','BCT15X','SCANNER-ANALOG','Scanner Analog'),
 ('EASTRENFREW','SCANNER-ANALOG','BCT15X','SCANNER-ANALOG','Scanner Analog'),
 ('RENFREW-EMS','SCANNER-ANALOG','BCT15X','SCANNER-ANALOG','Scanner Analog'),
 ('RENFREW-FIRE','SCANNER-ANALOG','BCT15X','SCANNER-ANALOG','Scanner Analog'),
 ('SEARS','SEARS','SEARS','SEARS','GRNPetawawa'),
 ('LANARK','LANARK-FIRE','LANARK-FIRE','LANARK','lanark'),
 ('FRONTENAC','fleetnet-kingston','FLEETNET-KINGSTON','FLEETNET-KINGSTON','kingston')
), updated AS (
 UPDATE calls c SET sender_id=m.sender,receiver_id=m.receiver,system_id=m.system_id,system_name=m.system_name
 FROM mapping m WHERE c.sender_id='legacy-import' AND c.receiver_id=m.old_receiver RETURNING c.id
)
SELECT count(*) FROM updated;"

docker run --rm --network host -e CALL_RECORDER_DATABASE_URL postgres:17-alpine \
    psql "$CALL_RECORDER_DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "$priority_sql" >/dev/null
canonicalized=$(docker run --rm --network host -e CALL_RECORDER_DATABASE_URL postgres:17-alpine \
    psql "$CALL_RECORDER_DATABASE_URL" -q -v ON_ERROR_STOP=1 -Atc "$mapping_sql")
if [[ "$canonicalized" != "0" ]]; then
    status_sql="BEGIN;
DELETE FROM receiver_status_entries WHERE sender_id IN ('SCANNER-DIGITAL','SCANNER-ANALOG','SEARS','LANARK-FIRE','fleetnet-kingston','legacy-import');
INSERT INTO receiver_status_entries (sender_id,receiver_id,system_id,site_id,system_name,site_name,call_count,last_call_at)
SELECT sender_id,coalesce(receiver_id,''),system_id,coalesce(max(site_id),''),max(system_name),max(site_name),count(*),max(coalesce(completed_at,start_time))
FROM calls WHERE sender_id IN ('SCANNER-DIGITAL','SCANNER-ANALOG','SEARS','LANARK-FIRE','fleetnet-kingston') GROUP BY sender_id,coalesce(receiver_id,''),system_id;
COMMIT;"
    docker run --rm --network host -e CALL_RECORDER_DATABASE_URL postgres:17-alpine \
        psql "$CALL_RECORDER_DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "$status_sql" >/dev/null
fi
printf 'canonicalized_calls=%s\n' "$canonicalized"
