-- +goose Up

-- Notifications are delivered to external channels (email, Discord, Telegram,
-- Matrix, ntfy, Pushover, …) via an Apprise API sidecar (caronc/apprise-api).
-- The user pastes their Apprise URL into prefs; the dispatcher POSTs to the
-- operator-configured APPRISE_API_URL with that URL as the destination. The
-- previous `email_enabled` column was a placeholder for an SMTP path that
-- never shipped — renaming preserves the toggle's semantics (off by default)
-- while making the actual mechanism explicit.
ALTER TABLE notification_prefs
  RENAME COLUMN email_enabled TO apprise_enabled;

ALTER TABLE notification_prefs
  ADD COLUMN apprise_url TEXT;

-- +goose Down

ALTER TABLE notification_prefs
  DROP COLUMN apprise_url;

ALTER TABLE notification_prefs
  RENAME COLUMN apprise_enabled TO email_enabled;
