CREATE TABLE IF NOT EXISTS remote_senders (
  sender_id text PRIMARY KEY CHECK (length(sender_id) BETWEEN 1 AND 100),
  key_hash bytea NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS calls (
  id text PRIMARY KEY,
  sender_id text NOT NULL REFERENCES remote_senders(sender_id), source_call_id text, receiver_id text,
  system_id text NOT NULL, system_name text, site_id text, site_name text,
  talkgroup_id text NOT NULL, talkgroup_name text, talkgroup_tag text,
  radio_id text, radio_name text, radio_tag text, frequency text, lcn text, voice_service text, call_type text,
  group_call boolean, audio_offset_ms bigint, start_time timestamptz NOT NULL, duration_ms bigint NOT NULL CHECK (duration_ms > 0),
  transcript text, notes text, audio_format text NOT NULL CHECK (audio_format IN ('mp3','wav')), audio_path text NOT NULL UNIQUE,
  audio_size bigint NOT NULL CHECK (audio_size > 0), audio_sha256 bytea NOT NULL, completed_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS calls_sender_source_unique ON calls(sender_id, source_call_id) WHERE source_call_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS calls_time_idx ON calls(start_time DESC);
CREATE INDEX IF NOT EXISTS calls_duplicate_idx ON calls(system_id,talkgroup_id,site_id,radio_id,start_time DESC);
CREATE TABLE IF NOT EXISTS call_targets (call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE, talkgroup_id text NOT NULL, talkgroup_name text, PRIMARY KEY(call_id,talkgroup_id));
CREATE TABLE IF NOT EXISTS pending_uploads (
  id text PRIMARY KEY, token_hash bytea NOT NULL UNIQUE, sender_id text NOT NULL REFERENCES remote_senders(sender_id),
  idempotency_key text, metadata jsonb NOT NULL, audio_format text NOT NULL CHECK (audio_format IN ('mp3','wav')),
  status text NOT NULL CHECK (status IN ('pending','completed','duplicate','expired')), expires_at timestamptz NOT NULL,
  completed_at timestamptz, completed_call_id text REFERENCES calls(id), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS pending_uploads_sender_idempotency_key_key ON pending_uploads(sender_id,idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS pending_uploads_expiry_idx ON pending_uploads(status,expires_at);
