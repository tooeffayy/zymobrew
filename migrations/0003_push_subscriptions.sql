-- +goose Up

ALTER TABLE push_devices
  ADD COLUMN p256dh TEXT,
  ADD COLUMN auth   TEXT;

-- +goose Down

ALTER TABLE push_devices
  DROP COLUMN IF EXISTS p256dh,
  DROP COLUMN IF EXISTS auth;
