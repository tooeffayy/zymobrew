-- +goose Up

-- Consolidated migration covering four related changes that landed together on
-- the smtp-notifications branch (formerly 0010-0013, merged pre-release):
--   1. Apprise external-notification channel
--   2. Direct-SMTP email channel
--   3. user_prefs table (split non-notification prefs off notification_prefs)
--   4. email_change_requests table (verified email-change flow)

-- (1) Apprise channel. Notifications are delivered to external channels (email,
-- Discord, Telegram, Matrix, ntfy, Pushover, …) via an Apprise API sidecar
-- (caronc/apprise-api). The user pastes their Apprise URL into prefs; the
-- dispatcher POSTs to the operator-configured APPRISE_API_URL with that URL as
-- the destination. notification_prefs.email_enabled was a placeholder for an
-- SMTP path that hadn't shipped — renaming it preserves the toggle's semantics
-- (off by default) while making the actual mechanism explicit.
ALTER TABLE notification_prefs
  RENAME COLUMN email_enabled TO apprise_enabled;

ALTER TABLE notification_prefs
  ADD COLUMN apprise_url TEXT;

-- (2) Direct SMTP delivery alongside Apprise. Apprise covers email via
-- mailto://, but plenty of self-hosters don't want to run the sidecar just to
-- get reminder emails — this gives them a built-in path. Operator configures
-- one SMTP relay via env (SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/TLS_MODE);
-- per-user opt-in via this flag. Destination is users.email, no separate
-- notification address — keeps the prefs UI single-field.
ALTER TABLE notification_prefs
  ADD COLUMN email_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- (3) User-level preferences that aren't credentials/PII (those live on
-- `users`) and aren't notification-delivery state (that lives on
-- `notification_prefs`). One row per user, PK = users.id, so the row's
-- existence is the upsert key and ON DELETE CASCADE wipes it with the user.
-- `timezone` previously lived on notification_prefs; it's hoisted here because
-- it now drives display/scheduling in places that don't otherwise need the
-- notification row (e.g. future calendar export).
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

-- (4) Pending email-change requests. A change is initiated (after password
-- re-auth) but does NOT touch users.email until the owner clicks a
-- confirmation link mailed to the NEW address — that proves the new address is
-- real and controlled by the requester. A second token is mailed to the OLD
-- address so the legitimate owner can cancel a change they didn't start (the
-- tripwire against a hijacked session quietly swapping the email).
--
-- Two independent capability tokens per request, each stored only as a SHA-256
-- hash (same scheme as sessions.token_hash) so a DB read can't replay a link.
-- new_email is CITEXT to match users.email's case-insensitive uniqueness; the
-- real uniqueness check happens against users at confirm time, since another
-- account could claim the address in the interim.
CREATE TABLE email_change_requests (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    new_email          CITEXT NOT NULL,
    old_email          CITEXT NOT NULL,
    confirm_token_hash TEXT NOT NULL UNIQUE,
    cancel_token_hash  TEXT NOT NULL UNIQUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_email_change_requests_user ON email_change_requests(user_id);

-- +goose Down

DROP TABLE email_change_requests;

ALTER TABLE notification_prefs
  ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

UPDATE notification_prefs np
SET timezone = up.timezone
FROM user_prefs up
WHERE up.user_id = np.user_id;

DROP TABLE IF EXISTS user_prefs;

ALTER TABLE notification_prefs
  DROP COLUMN email_enabled;

ALTER TABLE notification_prefs
  DROP COLUMN apprise_url;

ALTER TABLE notification_prefs
  RENAME COLUMN apprise_enabled TO email_enabled;
