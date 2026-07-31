-- 008_users_auth.sql
-- Creates users table for username/password authentication.
-- Replaces the single-user admin token system with multi-user accounts.

CREATE TABLE IF NOT EXISTS users (
  id bigserial PRIMARY KEY,
  username text UNIQUE NOT NULL CHECK (length(username) BETWEEN 1 AND 100),
  password_hash text NOT NULL,  -- Argon2id hash string
  role text NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin', 'viewer')),
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Index for login lookups
CREATE INDEX IF NOT EXISTS idx_users_username ON users (username) WHERE enabled = true;
