-- +goose Up
-- Makes rsync's --delete behavior (removing from the destination anything
-- that no longer exists at the source — a true mirror) an explicit,
-- per-job opt-in/opt-out instead of always-on. Defaults to true to
-- preserve the exact behavior every existing job already had.
ALTER TABLE backup_jobs ADD COLUMN delete_extraneous BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE backup_jobs DROP COLUMN delete_extraneous;
