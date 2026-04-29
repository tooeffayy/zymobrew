-- +goose Up

ALTER TABLE user_exports  ADD COLUMN sha256 TEXT;
ALTER TABLE admin_backups ADD COLUMN sha256 TEXT;

-- +goose Down

ALTER TABLE user_exports  DROP COLUMN sha256;
ALTER TABLE admin_backups DROP COLUMN sha256;
