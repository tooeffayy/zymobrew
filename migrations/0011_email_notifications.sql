-- +goose Up

-- Direct SMTP delivery alongside Apprise. Apprise covers email via mailto://,
-- but plenty of self-hosters don't want to run the sidecar just to get
-- reminder emails — this gives them a built-in path. Operator configures one
-- SMTP relay via env (SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/TLS_MODE);
-- per-user opt-in via this flag. Destination is users.email, no separate
-- notification address — keeps the prefs UI single-field.
ALTER TABLE notification_prefs
  ADD COLUMN email_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down

ALTER TABLE notification_prefs
  DROP COLUMN email_enabled;
