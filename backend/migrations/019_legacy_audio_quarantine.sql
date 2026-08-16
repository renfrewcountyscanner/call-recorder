ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS decode_failure_count integer NOT NULL DEFAULT 0;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS decode_failure_first_at timestamptz;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS rejected_audio_sha256 bytea;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS quarantine_path text;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS quarantined_at timestamptz;

ALTER TABLE pending_uploads DROP CONSTRAINT IF EXISTS pending_uploads_status_check;
ALTER TABLE pending_uploads ADD CONSTRAINT pending_uploads_status_check
  CHECK(status IN ('pending','uploading','completed','duplicate','expired','failed','quarantined'));

CREATE INDEX IF NOT EXISTS pending_uploads_quarantined_idx
  ON pending_uploads(quarantined_at DESC) WHERE status='quarantined';
