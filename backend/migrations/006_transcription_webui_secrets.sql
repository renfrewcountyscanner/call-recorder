-- Encrypted application secrets and human-readable transcription settings.
CREATE TABLE IF NOT EXISTS application_secrets (
  id bigserial PRIMARY KEY,
  purpose text NOT NULL,
  display_name text NOT NULL,
  ciphertext bytea NOT NULL,
  nonce bytea NOT NULL,
  encryption_version integer NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(purpose)
);
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS min_duration_ms bigint NOT NULL DEFAULT 0;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS temperature numeric NOT NULL DEFAULT 0;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS vad_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS phrase_prompts_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS phrase_prompt text;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS request_timeout_seconds integer NOT NULL DEFAULT 60;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS allowed_endpoint_cidrs text NOT NULL DEFAULT '';
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS processing_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE application_secrets ADD COLUMN IF NOT EXISTS updated_by text;
CREATE INDEX IF NOT EXISTS application_secrets_purpose_idx ON application_secrets(purpose);
CREATE TABLE IF NOT EXISTS transcription_worker_heartbeat (
  id boolean PRIMARY KEY DEFAULT true,
  worker_id text NOT NULL DEFAULT 'transcription-worker',
  heartbeat_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
