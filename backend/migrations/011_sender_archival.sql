-- Sender credentials may be removed from active administration without
-- deleting historical calls that still reference their sender IDs.
ALTER TABLE remote_senders ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
CREATE INDEX IF NOT EXISTS remote_senders_active_idx ON remote_senders(sender_id) WHERE deleted_at IS NULL;
