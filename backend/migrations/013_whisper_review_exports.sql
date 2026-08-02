-- Review-safe Whisper dataset exports and notification backlog protection.

UPDATE transcripts SET review_status='unreviewed' WHERE review_status='needs_review';
ALTER TABLE transcripts DROP CONSTRAINT IF EXISTS transcripts_review_status_check;
ALTER TABLE transcripts ADD CONSTRAINT transcripts_review_status_check
  CHECK(review_status IN ('unreviewed','reviewed','rejected','inaudible','no_speech'));
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS review_notes text;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS timed_segments jsonb;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS profile text;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS settings_version bigint;

ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS profile text;
ALTER TABLE transcription_config ADD COLUMN IF NOT EXISTS settings_version bigint NOT NULL DEFAULT 1;

ALTER TABLE dataset_exports ADD COLUMN IF NOT EXISTS export_type text NOT NULL DEFAULT 'asr_finetune';
ALTER TABLE dataset_exports DROP CONSTRAINT IF EXISTS dataset_exports_export_type_check;
ALTER TABLE dataset_exports ADD CONSTRAINT dataset_exports_export_type_check
  CHECK(export_type IN ('asr_finetune','hallucination_evaluation'));
ALTER TABLE dataset_exports ADD COLUMN IF NOT EXISTS schema_version integer NOT NULL DEFAULT 2;

ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS label_source text;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS reviewed_at timestamptz;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS reviewer text;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS review_notes text;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS conversation_group_id text;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS profile text;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS settings_version bigint;
ALTER TABLE dataset_export_items ADD COLUMN IF NOT EXISTS timed_segments jsonb;
ALTER TABLE dataset_export_items DROP CONSTRAINT IF EXISTS dataset_export_items_label_source_check;
ALTER TABLE dataset_export_items ADD CONSTRAINT dataset_export_items_label_source_check
  CHECK(label_source IS NULL OR label_source IN ('human_edited','generated','received'));
CREATE INDEX IF NOT EXISTS dataset_export_items_conversation_idx
  ON dataset_export_items(export_id,conversation_group_id,split);

ALTER TABLE notification_deliveries DROP CONSTRAINT IF EXISTS notification_deliveries_status_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_status_check
  CHECK(status IN ('pending','sending','sent','failed','expired'));
INSERT INTO application_settings(setting_key,setting_value)
VALUES ('notification_max_age_minutes','15'::jsonb)
ON CONFLICT(setting_key) DO NOTHING;
