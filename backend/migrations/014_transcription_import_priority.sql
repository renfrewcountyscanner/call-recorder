-- Imported historical audio must never delay live receiver transcription.
ALTER TABLE transcription_jobs ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS transcription_jobs_priority_queue_idx ON transcription_jobs(status,priority DESC,next_attempt_at,id);
