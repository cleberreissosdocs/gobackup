-- +goose Up
-- Exclusion patterns for job_kind=files backups: rsync --exclude syntax,
-- one pattern per line (empty string = exclude nothing, the previous and
-- still-default behavior for every existing job).
ALTER TABLE backup_jobs ADD COLUMN exclude_patterns TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backup_jobs DROP COLUMN exclude_patterns;
