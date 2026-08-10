-- Notification alerts are transcript-driven. Metadata fields narrow a rule;
-- they never generate an alert by themselves.
UPDATE notification_rules
SET enabled=false, updated_at=now()
WHERE NULLIF(btrim(keyword),'') IS NULL;

ALTER TABLE notification_rules
  DROP CONSTRAINT IF EXISTS notification_rules_keyword_required_check;
ALTER TABLE notification_rules
  ADD CONSTRAINT notification_rules_keyword_required_check
  CHECK(NULLIF(btrim(keyword),'') IS NOT NULL);
