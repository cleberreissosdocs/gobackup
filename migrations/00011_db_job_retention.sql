-- +goose Up
-- Retention for database backup jobs: automatically delete this job's own
-- past backup files once they're older than N days (0 = keep forever,
-- the previous implicit behavior). File jobs are unaffected — they always
-- mirror into one continuously-updated destination, so there's nothing to
-- accumulate/retain there.
ALTER TABLE backup_jobs ADD COLUMN retention_days INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE backup_jobs DROP COLUMN retention_days;
