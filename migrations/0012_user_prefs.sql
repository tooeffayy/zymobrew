-- +goose Up

-- User-level preferences that aren't credentials/PII (those live on `users`)
-- and aren't notification-delivery state (that lives on `notification_prefs`).
-- One row per user, PK = users.id, so the row's existence is the upsert key
-- and ON DELETE CASCADE wipes it with the user.
--
-- `timezone` previously lived on notification_prefs; it's hoisted here
-- because it now drives display/scheduling in places that don't otherwise
-- need the notification row (e.g. future calendar export).
CREATE TABLE user_prefs (
    user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    degree_units TEXT NOT NULL DEFAULT 'C' CHECK (degree_units IN ('C', 'F')),
    timezone     TEXT NOT NULL DEFAULT 'UTC'
);

-- Backfill: every existing user gets a row, carrying their current
-- notification_prefs.timezone if one exists.
INSERT INTO user_prefs (user_id, timezone)
SELECT u.id, COALESCE(np.timezone, 'UTC')
FROM users u
LEFT JOIN notification_prefs np ON np.user_id = u.id;

ALTER TABLE notification_prefs DROP COLUMN timezone;

-- +goose Down

ALTER TABLE notification_prefs
  ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

UPDATE notification_prefs np
SET timezone = up.timezone
FROM user_prefs up
WHERE up.user_id = np.user_id;

DROP TABLE IF EXISTS user_prefs;
