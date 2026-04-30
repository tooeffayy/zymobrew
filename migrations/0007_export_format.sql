-- +goose Up

ALTER TABLE user_exports ADD COLUMN format TEXT NOT NULL DEFAULT 'zip';

-- +goose Down

ALTER TABLE user_exports DROP COLUMN format;
