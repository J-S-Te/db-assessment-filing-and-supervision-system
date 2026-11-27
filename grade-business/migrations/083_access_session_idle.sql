ALTER TABLE access_sessions ADD COLUMN IF NOT EXISTS last_seen_at timestamptz NOT NULL DEFAULT statement_timestamp();
