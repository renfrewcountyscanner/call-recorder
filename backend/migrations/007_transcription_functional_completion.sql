-- Transcription functional completion: provider type, test results, and retry audit.
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS provider_type text NOT NULL DEFAULT 'faster-whisper' CHECK (provider_type IN ('openai-compatible', 'faster-whisper'));
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS last_test_at timestamptz;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS last_test_ok boolean;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS last_test_error text;

ALTER TABLE transcription_jobs ADD COLUMN IF NOT EXISTS retry_identity text;
ALTER TABLE transcription_jobs ADD COLUMN IF NOT EXISTS retry_at timestamptz;
ALTER TABLE transcription_jobs ADD COLUMN IF NOT EXISTS retry_count integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS transcription_worker_heartbeat_idx ON transcription_worker_heartbeat(heartbeat_at);
