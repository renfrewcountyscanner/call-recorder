-- Call Recorder 1.0 foundations. This migration is additive so the previous
-- application image can still be used during rollback.

CREATE TABLE IF NOT EXISTS schema_migrations (
  filename text PRIMARY KEY,
  checksum_sha256 text NOT NULL,
  release_version text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS application_settings (
  setting_key text PRIMARY KEY,
  setting_value jsonb NOT NULL,
  updated_by text,
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO application_settings(setting_key,setting_value)
VALUES ('receiver_stale_minutes','15'::jsonb)
ON CONFLICT(setting_key) DO NOTHING;

CREATE TABLE IF NOT EXISTS audit_events (
  id bigserial PRIMARY KEY,
  actor text NOT NULL,
  action text NOT NULL,
  target_type text NOT NULL,
  target_id text,
  request_id text,
  details jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_events_target_idx
  ON audit_events(target_type,target_id,created_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor_idx
  ON audit_events(actor,created_at DESC);

CREATE TABLE IF NOT EXISTS user_sessions (
  id text PRIMARY KEY,
  token_hash bytea NOT NULL UNIQUE,
  username text NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  csrf_token_hash bytea NOT NULL,
  remember_me boolean NOT NULL DEFAULT false,
  expires_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_sessions_active_idx
  ON user_sessions(username,expires_at) WHERE revoked_at IS NULL;

ALTER TABLE remote_senders ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;
ALTER TABLE remote_senders ADD COLUMN IF NOT EXISTS last_error_at timestamptz;
ALTER TABLE remote_senders ADD COLUMN IF NOT EXISTS last_error text;

CREATE TABLE IF NOT EXISTS receiver_status_entries (
  sender_id text NOT NULL REFERENCES remote_senders(sender_id),
  receiver_id text NOT NULL DEFAULT '',
  system_id text NOT NULL,
  site_id text NOT NULL DEFAULT '',
  system_name text,
  site_name text,
  call_count bigint NOT NULL DEFAULT 0 CHECK(call_count >= 0),
  last_call_at timestamptz NOT NULL,
  dismissed_at timestamptz,
  dismissed_by text,
  dismissed_last_call_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(sender_id,receiver_id,system_id,site_id)
);
CREATE INDEX IF NOT EXISTS receiver_status_activity_idx
  ON receiver_status_entries(last_call_at DESC);
CREATE INDEX IF NOT EXISTS receiver_status_visible_idx
  ON receiver_status_entries(last_call_at DESC) WHERE dismissed_at IS NULL;

INSERT INTO receiver_status_entries(
  sender_id,receiver_id,system_id,site_id,system_name,site_name,call_count,last_call_at
)
SELECT sender_id,coalesce(receiver_id,''),system_id,coalesce(site_id,''),
       max(system_name),max(site_name),count(*),max(start_time)
FROM calls
GROUP BY sender_id,coalesce(receiver_id,''),system_id,coalesce(site_id,'')
ON CONFLICT(sender_id,receiver_id,system_id,site_id) DO UPDATE SET
  system_name=coalesce(EXCLUDED.system_name,receiver_status_entries.system_name),
  site_name=coalesce(EXCLUDED.site_name,receiver_status_entries.site_name),
  call_count=GREATEST(receiver_status_entries.call_count,EXCLUDED.call_count),
  last_call_at=GREATEST(receiver_status_entries.last_call_at,EXCLUDED.last_call_at),
  updated_at=now();

ALTER TABLE talkgroup_aliases ADD COLUMN IF NOT EXISTS transcription_mode text;
UPDATE talkgroup_aliases
SET transcription_mode=CASE WHEN transcription_enabled THEN 'enabled' ELSE 'disabled' END
WHERE transcription_mode IS NULL;
ALTER TABLE talkgroup_aliases ALTER COLUMN transcription_mode SET DEFAULT 'inherit';
ALTER TABLE talkgroup_aliases ALTER COLUMN transcription_mode SET NOT NULL;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='talkgroup_aliases_transcription_mode_check') THEN
    ALTER TABLE talkgroup_aliases ADD CONSTRAINT talkgroup_aliases_transcription_mode_check
      CHECK(transcription_mode IN ('inherit','enabled','disabled'));
  END IF;
END $$;

ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS model text;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS review_status text NOT NULL DEFAULT 'unreviewed';
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS reviewed_at timestamptz;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS reviewed_by text;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='transcripts_review_status_check') THEN
    ALTER TABLE transcripts ADD CONSTRAINT transcripts_review_status_check
      CHECK(review_status IN ('unreviewed','reviewed','rejected','needs_review'));
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS transcripts_review_idx ON transcripts(review_status,updated_at DESC);

ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS lease_owner text;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 0;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS last_error text;
ALTER TABLE pending_uploads ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE pending_uploads DROP CONSTRAINT IF EXISTS pending_uploads_status_check;
ALTER TABLE pending_uploads ADD CONSTRAINT pending_uploads_status_check
  CHECK(status IN ('pending','uploading','completed','duplicate','expired','failed'));
CREATE INDEX IF NOT EXISTS pending_uploads_lease_idx
  ON pending_uploads(status,lease_expires_at);

CREATE TABLE IF NOT EXISTS dataset_exports (
  id text PRIMARY KEY,
  requested_by text NOT NULL,
  filters jsonb NOT NULL DEFAULT '{}'::jsonb,
  status text NOT NULL DEFAULT 'pending'
    CHECK(status IN ('pending','running','completed','completed_with_warnings','failed','cancelled','expired')),
  total_items integer NOT NULL DEFAULT 0 CHECK(total_items >= 0),
  processed_items integer NOT NULL DEFAULT 0 CHECK(processed_items >= 0),
  warning_count integer NOT NULL DEFAULT 0 CHECK(warning_count >= 0),
  estimated_bytes bigint NOT NULL DEFAULT 0 CHECK(estimated_bytes >= 0),
  output_path text,
  output_size bigint,
  output_sha256 bytea,
  error text,
  lease_owner text,
  lease_expires_at timestamptz,
  expires_at timestamptz NOT NULL DEFAULT now() + interval '24 hours',
  started_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS dataset_exports_queue_idx
  ON dataset_exports(status,created_at);
CREATE INDEX IF NOT EXISTS dataset_exports_expiry_idx
  ON dataset_exports(expires_at) WHERE status IN ('completed','completed_with_warnings','failed','cancelled');

CREATE TABLE IF NOT EXISTS dataset_export_items (
  export_id text NOT NULL REFERENCES dataset_exports(id) ON DELETE CASCADE,
  call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
  transcript_id bigint REFERENCES transcripts(id) ON DELETE SET NULL,
  effective_text text NOT NULL,
  received_text text,
  generated_text text,
  edited_text text,
  review_status text NOT NULL DEFAULT 'unreviewed',
  language text,
  provider text,
  model text,
  split text NOT NULL CHECK(split IN ('train','validation','test')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(export_id,call_id)
);
ALTER TABLE dataset_export_items DROP CONSTRAINT IF EXISTS dataset_export_items_call_id_fkey;
ALTER TABLE dataset_export_items ADD CONSTRAINT dataset_export_items_call_id_fkey FOREIGN KEY(call_id) REFERENCES calls(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS dataset_export_items_call_idx ON dataset_export_items(call_id);

CREATE TABLE IF NOT EXISTS notification_worker_heartbeat (
  id boolean PRIMARY KEY DEFAULT true,
  worker_id text NOT NULL DEFAULT 'notification-worker',
  heartbeat_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE retention_runs ADD COLUMN IF NOT EXISTS audio_bytes_matched bigint NOT NULL DEFAULT 0;
ALTER TABLE retention_runs ADD COLUMN IF NOT EXISTS audio_duration_ms_matched bigint NOT NULL DEFAULT 0;
ALTER TABLE retention_runs ADD COLUMN IF NOT EXISTS error text;

-- Prevent destructive retention from removing calls while an export snapshot
-- is queued or running. Application queries use this index through EXISTS.
CREATE INDEX IF NOT EXISTS dataset_export_items_export_idx ON dataset_export_items(export_id,call_id);
