CREATE TABLE IF NOT EXISTS talkgroup_aliases (
  id bigserial PRIMARY KEY, system_id text NOT NULL, talkgroup_id text NOT NULL,
  alias text, description text, category text, priority integer NOT NULL DEFAULT 0,
  enabled boolean NOT NULL DEFAULT true, source text NOT NULL CHECK (source IN ('received','manual','imported')),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(system_id, talkgroup_id)
);
CREATE TABLE IF NOT EXISTS radio_aliases (
  id bigserial PRIMARY KEY, system_id text NOT NULL, radio_id text NOT NULL,
  alias text, description text, category text, enabled boolean NOT NULL DEFAULT true,
  source text NOT NULL CHECK (source IN ('received','manual','imported')),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(system_id, radio_id)
);
CREATE TABLE IF NOT EXISTS retention_policies (
  id bigserial PRIMARY KEY, name text NOT NULL UNIQUE, enabled boolean NOT NULL DEFAULT false,
  retention_days integer NOT NULL CHECK (retention_days > 0), sender_filter text, system_filter text,
  talkgroup_filter text, call_type_filter text, min_duration_ms bigint, max_duration_ms bigint,
  priority integer NOT NULL DEFAULT 0, dry_run boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS retention_runs (
  id bigserial PRIMARY KEY, policy_id bigint REFERENCES retention_policies(id) ON DELETE SET NULL,
  started_at timestamptz NOT NULL DEFAULT now(), ended_at timestamptz, dry_run boolean NOT NULL,
  calls_matched integer NOT NULL DEFAULT 0, calls_deleted integer NOT NULL DEFAULT 0,
  audio_files_deleted integer NOT NULL DEFAULT 0, failures integer NOT NULL DEFAULT 0, summary jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS talkgroup_aliases_system_idx ON talkgroup_aliases(system_id,talkgroup_id);
CREATE INDEX IF NOT EXISTS radio_aliases_system_idx ON radio_aliases(system_id,radio_id);
CREATE INDEX IF NOT EXISTS retention_policies_priority_idx ON retention_policies(enabled,priority DESC);
