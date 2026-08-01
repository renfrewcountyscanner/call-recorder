-- 009_editor_role.sql
-- Adds the operational editor role.

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'users_role_check'
      AND conrelid = 'users'::regclass
  ) THEN
    ALTER TABLE users ADD CONSTRAINT users_role_check
      CHECK (role IN ('admin', 'editor', 'viewer'));
  END IF;
END $$;
