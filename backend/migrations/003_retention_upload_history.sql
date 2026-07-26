-- Retention keeps upload history but releases its reference to deleted calls.
ALTER TABLE pending_uploads DROP CONSTRAINT IF EXISTS pending_uploads_completed_call_id_fkey;
ALTER TABLE pending_uploads
  ADD CONSTRAINT pending_uploads_completed_call_id_fkey
  FOREIGN KEY (completed_call_id) REFERENCES calls(id) ON DELETE SET NULL;
