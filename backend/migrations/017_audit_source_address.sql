ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS source_address text;
CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events(created_at DESC);
