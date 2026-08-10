-- Merge case-conflicting sender IDs and force all sender IDs to uppercase.
-- This migration is idempotent: running it again after the data is already
-- uppercase is a no-op.

-- Allow sender_id renames to cascade into child tables during this migration.
ALTER TABLE calls DROP CONSTRAINT IF EXISTS calls_sender_id_fkey;
ALTER TABLE pending_uploads DROP CONSTRAINT IF EXISTS pending_uploads_sender_id_fkey;
ALTER TABLE receiver_status_entries DROP CONSTRAINT IF EXISTS receiver_status_entries_sender_id_fkey;

-- Normalize collision-prone child rows before changing sender IDs. Pending
-- uploads can share an idempotency key after case folding; keep the most useful
-- and most recently updated row. Receiver status is rebuilt from an aggregate
-- so its composite primary key cannot collide during the rename.
DELETE FROM pending_uploads p
USING (
    SELECT id
    FROM (
        SELECT id,
               row_number() OVER (
                   PARTITION BY upper(sender_id), idempotency_key
                   ORDER BY CASE status WHEN 'completed' THEN 0 WHEN 'duplicate' THEN 1 ELSE 2 END,
                            updated_at DESC,
                            id
               ) AS position
        FROM pending_uploads
        WHERE idempotency_key IS NOT NULL
    ) ranked
    WHERE position > 1
) duplicate
WHERE p.id = duplicate.id;

CREATE TEMP TABLE merged_receiver_status_entries AS
SELECT upper(sender_id) AS sender_id,
       receiver_id,
       system_id,
       site_id,
       max(system_name) AS system_name,
       max(site_name) AS site_name,
       sum(call_count) AS call_count,
       max(last_call_at) AS last_call_at,
       max(dismissed_at) AS dismissed_at,
       max(dismissed_by) AS dismissed_by,
       max(dismissed_last_call_at) AS dismissed_last_call_at,
       min(created_at) AS created_at,
       max(updated_at) AS updated_at
FROM receiver_status_entries
GROUP BY upper(sender_id),receiver_id,system_id,site_id;
TRUNCATE receiver_status_entries;

DO $$
DECLARE
    grp RECORD;
    canonical_row RECORD;
BEGIN
    -- Process each group of sender_ids that differ only by case.
    FOR grp IN
        SELECT upper(sender_id) AS canonical_id_base, array_agg(sender_id) AS ids
        FROM remote_senders
        WHERE deleted_at IS NULL
        GROUP BY upper(sender_id)
        HAVING count(*) > 1
    LOOP
        -- Prefer an existing exact-uppercase row as the canonical record.
        SELECT * INTO canonical_row
        FROM remote_senders
        WHERE sender_id = grp.canonical_id_base AND deleted_at IS NULL;

        IF canonical_row IS NULL THEN
            -- No exact uppercase row exists; keep the alphabetically first row
            -- and rename it to the uppercase canonical form.
            SELECT * INTO canonical_row
            FROM remote_senders
            WHERE sender_id = ANY(grp.ids) AND deleted_at IS NULL
            ORDER BY sender_id
            LIMIT 1;

            UPDATE remote_senders
            SET sender_id = grp.canonical_id_base
            WHERE sender_id = canonical_row.sender_id;
        END IF;

        -- Point all related rows to the canonical sender ID.
        UPDATE calls SET sender_id = grp.canonical_id_base
        WHERE sender_id = ANY(grp.ids) AND sender_id != grp.canonical_id_base;

        UPDATE pending_uploads SET sender_id = grp.canonical_id_base
        WHERE sender_id = ANY(grp.ids) AND sender_id != grp.canonical_id_base;

        UPDATE receiver_status_entries SET sender_id = grp.canonical_id_base
        WHERE sender_id = ANY(grp.ids) AND sender_id != grp.canonical_id_base;

        -- Remove the duplicate sender rows.
        DELETE FROM remote_senders
        WHERE sender_id = ANY(grp.ids) AND sender_id != grp.canonical_id_base;
    END LOOP;

    -- Uppercase any remaining non-duplicate sender IDs.
    -- remote_senders must be updated first so FK checks on the child tables pass.
    UPDATE remote_senders SET sender_id = upper(sender_id)
    WHERE deleted_at IS NULL AND sender_id != upper(sender_id);

    UPDATE calls SET sender_id = upper(sender_id) WHERE sender_id != upper(sender_id);
    UPDATE pending_uploads SET sender_id = upper(sender_id) WHERE sender_id != upper(sender_id);
    UPDATE receiver_status_entries SET sender_id = upper(sender_id) WHERE sender_id != upper(sender_id);
END $$;

INSERT INTO receiver_status_entries(
    sender_id,receiver_id,system_id,site_id,system_name,site_name,call_count,
    last_call_at,dismissed_at,dismissed_by,dismissed_last_call_at,created_at,updated_at
)
SELECT sender_id,receiver_id,system_id,site_id,system_name,site_name,call_count,
       last_call_at,dismissed_at,dismissed_by,dismissed_last_call_at,created_at,updated_at
FROM merged_receiver_status_entries;
DROP TABLE merged_receiver_status_entries;

-- Prevent future case-duplicates at the database level.
CREATE UNIQUE INDEX IF NOT EXISTS remote_senders_lower_idx
    ON remote_senders(lower(sender_id))
    WHERE deleted_at IS NULL;

-- Ensure every sender_id in remote_senders is uppercase before adding FK constraints.
UPDATE remote_senders SET sender_id = upper(sender_id)
WHERE deleted_at IS NULL AND sender_id != upper(sender_id);

-- Create placeholder sender rows for any orphaned call/pending/receiver references.
-- This can happen if calls were ingested from a sender that was later deleted
-- or never recorded in remote_senders.
INSERT INTO remote_senders (sender_id, key_hash, enabled, created_at)
SELECT DISTINCT upper(c.sender_id), '\x00'::bytea, false, now()
FROM calls c
LEFT JOIN remote_senders r ON r.sender_id = c.sender_id
WHERE r.sender_id IS NULL
ON CONFLICT (sender_id) DO NOTHING;

INSERT INTO remote_senders (sender_id, key_hash, enabled, created_at)
SELECT DISTINCT upper(p.sender_id), '\x00'::bytea, false, now()
FROM pending_uploads p
LEFT JOIN remote_senders r ON r.sender_id = p.sender_id
WHERE r.sender_id IS NULL
ON CONFLICT (sender_id) DO NOTHING;

INSERT INTO remote_senders (sender_id, key_hash, enabled, created_at)
SELECT DISTINCT upper(rse.sender_id), '\x00'::bytea, false, now()
FROM receiver_status_entries rse
LEFT JOIN remote_senders r ON r.sender_id = rse.sender_id
WHERE r.sender_id IS NULL
ON CONFLICT (sender_id) DO NOTHING;

-- Restore FK constraints with cascade on update so future sender_id renames propagate.
ALTER TABLE calls ADD CONSTRAINT calls_sender_id_fkey
    FOREIGN KEY (sender_id) REFERENCES remote_senders(sender_id) ON UPDATE CASCADE;
ALTER TABLE pending_uploads ADD CONSTRAINT pending_uploads_sender_id_fkey
    FOREIGN KEY (sender_id) REFERENCES remote_senders(sender_id) ON UPDATE CASCADE;
ALTER TABLE receiver_status_entries ADD CONSTRAINT receiver_status_entries_sender_id_fkey
    FOREIGN KEY (sender_id) REFERENCES remote_senders(sender_id) ON UPDATE CASCADE;
