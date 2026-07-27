-- Phase 8 is additive and safe to reapply on a v0.3.0 database.
ALTER TABLE calls ADD COLUMN IF NOT EXISTS protected boolean NOT NULL DEFAULT false;
ALTER TABLE calls ADD COLUMN IF NOT EXISTS protection_reason text;
ALTER TABLE calls ADD COLUMN IF NOT EXISTS protected_at timestamptz;
ALTER TABLE calls ADD COLUMN IF NOT EXISTS protected_by text;
ALTER TABLE calls ADD COLUMN IF NOT EXISTS protection_expires_at timestamptz;
ALTER TABLE talkgroup_aliases ADD COLUMN IF NOT EXISTS transcription_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE talkgroup_aliases ADD COLUMN IF NOT EXISTS transcription_language text;
ALTER TABLE talkgroup_aliases ADD COLUMN IF NOT EXISTS notification_eligible boolean NOT NULL DEFAULT true;
CREATE TABLE IF NOT EXISTS protection_events (
  id bigserial PRIMARY KEY, call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
  protected boolean NOT NULL, reason text, identity text, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS protection_events_call_idx ON protection_events(call_id,created_at DESC);

CREATE TABLE IF NOT EXISTS favourite_groups (
  id bigserial PRIMARY KEY,
  name text NOT NULL UNIQUE,
  description text,
  enabled boolean NOT NULL DEFAULT true,
  display_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS favourite_members (
  group_id bigint NOT NULL REFERENCES favourite_groups(id) ON DELETE CASCADE,
  system_id text NOT NULL,
  talkgroup_id text NOT NULL,
  display_alias text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(group_id,system_id,talkgroup_id)
);
CREATE INDEX IF NOT EXISTS favourite_members_lookup_idx ON favourite_members(system_id,talkgroup_id);

CREATE TABLE IF NOT EXISTS notification_destinations (
  id bigserial PRIMARY KEY,
  name text NOT NULL UNIQUE,
  destination_type text NOT NULL CHECK(destination_type IN ('smtp','webhook','discord','telegram')),
  enabled boolean NOT NULL DEFAULT false,
  config jsonb NOT NULL DEFAULT '{}'::jsonb,
  secret_ref text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS notification_rules (
  id bigserial PRIMARY KEY,
  name text NOT NULL UNIQUE,
  enabled boolean NOT NULL DEFAULT false,
  destination_id bigint NOT NULL REFERENCES notification_destinations(id) ON DELETE CASCADE,
  priority integer NOT NULL DEFAULT 0,
  sender_filter text, system_filter text, site_filter text, talkgroup_filter text, radio_filter text,
  call_type_filter text, frequency_min numeric, frequency_max numeric,
  min_duration_ms bigint, max_duration_ms bigint, patched_only boolean NOT NULL DEFAULT false,
  keyword text, favourite_group_id bigint REFERENCES favourite_groups(id) ON DELETE SET NULL,
  template text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS notification_deliveries (
  id bigserial PRIMARY KEY,
  rule_id bigint NOT NULL REFERENCES notification_rules(id) ON DELETE CASCADE,
  destination_id bigint NOT NULL REFERENCES notification_destinations(id) ON DELETE CASCADE,
  call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
  status text NOT NULL CHECK(status IN ('pending','sending','sent','failed')) DEFAULT 'pending',
  attempt_count integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(), last_attempt_at timestamptz,
  successful_at timestamptz, error text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(rule_id,call_id)
);
CREATE INDEX IF NOT EXISTS notification_deliveries_queue_idx ON notification_deliveries(status,next_attempt_at);

CREATE TABLE IF NOT EXISTS transcription_config (
  id boolean PRIMARY KEY DEFAULT true,
  provider text NOT NULL DEFAULT 'openai-compatible', enabled boolean NOT NULL DEFAULT false,
  default_language text, max_audio_duration_ms bigint NOT NULL DEFAULT 900000,
  max_file_size bigint NOT NULL DEFAULT 52428800, concurrency integer NOT NULL DEFAULT 1,
  retry_limit integer NOT NULL DEFAULT 3, endpoint_url text, model text, secret_ref text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO transcription_config(id) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS transcription_jobs (
  id bigserial PRIMARY KEY,
  call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
  status text NOT NULL CHECK(status IN ('pending','running','completed','failed')) DEFAULT 'pending',
  provider text NOT NULL, attempt_count integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz, completed_at timestamptz, error text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(call_id,provider)
);
CREATE TABLE IF NOT EXISTS transcripts (
  id bigserial PRIMARY KEY,
  call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
  provider text NOT NULL, language text, text text NOT NULL, confidence numeric,
  original_text text, edited_text text, edited_at timestamptz, edited_by text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(call_id,provider)
);
CREATE INDEX IF NOT EXISTS transcription_jobs_queue_idx ON transcription_jobs(status,next_attempt_at);
CREATE INDEX IF NOT EXISTS transcripts_call_idx ON transcripts(call_id);

-- Keep the existing full-text index useful for generated and edited transcripts.
CREATE OR REPLACE FUNCTION calls_search_document_refresh() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.search_document := to_tsvector('simple'::regconfig,
    coalesce(NEW.system_id,'') || ' ' || coalesce(NEW.system_name,'') || ' ' || coalesce(NEW.site_id,'') || ' ' || coalesce(NEW.site_name,'') || ' ' ||
    coalesce(NEW.receiver_id,'') || ' ' || coalesce(NEW.talkgroup_id,'') || ' ' || coalesce(NEW.talkgroup_name,'') || ' ' || coalesce(NEW.radio_id,'') || ' ' ||
    coalesce(NEW.radio_name,'') || ' ' || coalesce(NEW.frequency,'') || ' ' || coalesce(NEW.lcn,'') || ' ' || coalesce(NEW.call_type,'') || ' ' ||
    coalesce(NEW.transcript,'') || ' ' || coalesce(NEW.notes,''));
  RETURN NEW;
END; $$;
UPDATE calls c SET search_document = to_tsvector('simple'::regconfig,
  coalesce(c.system_id,'') || ' ' || coalesce(c.system_name,'') || ' ' || coalesce(c.site_id,'') || ' ' || coalesce(c.site_name,'') || ' ' ||
  coalesce(c.receiver_id,'') || ' ' || coalesce(c.talkgroup_id,'') || ' ' || coalesce(c.talkgroup_name,'') || ' ' || coalesce(c.radio_id,'') || ' ' ||
  coalesce(c.radio_name,'') || ' ' || coalesce(c.frequency,'') || ' ' || coalesce(c.lcn,'') || ' ' || coalesce(c.call_type,'') || ' ' ||
  coalesce(c.transcript,'') || ' ' || coalesce(c.notes,'') || ' ' || coalesce((SELECT string_agg(coalesce(t.edited_text,t.text),' ') FROM transcripts t WHERE t.call_id=c.id),'')
);
