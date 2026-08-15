CREATE TABLE IF NOT EXISTS ingestion_rejection_counters (
  bucket timestamptz NOT NULL,
  sender_id text NOT NULL DEFAULT '',
  reason text NOT NULL,
  rejection_count bigint NOT NULL DEFAULT 0 CHECK(rejection_count >= 0),
  last_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(bucket,sender_id,reason)
);

CREATE INDEX IF NOT EXISTS ingestion_rejection_counters_recent_idx
  ON ingestion_rejection_counters(last_at DESC);
