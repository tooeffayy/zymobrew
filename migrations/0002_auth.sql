-- +goose Up

ALTER TABLE users ADD COLUMN password_hash TEXT;

CREATE TABLE sessions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL UNIQUE,
  user_agent   TEXT,
  ip           INET,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user_idx       ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);


-- +goose Down

DROP TABLE IF EXISTS sessions;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
