-- +goose Up
ALTER TABLE users ADD COLUMN has_avatar INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN has_avatar;
