-- +goose Up
ALTER TABLE agents ADD COLUMN notified_offline_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE agents DROP COLUMN notified_offline_at;
