-- Additive transcription administration preferences; all preserve disabled defaults.
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS min_audio_duration_ms bigint NOT NULL DEFAULT 0;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS request_timeout_seconds integer NOT NULL DEFAULT 60;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS temperature numeric;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS vad_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS phrase_prompts_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS phrase_prompts text;
