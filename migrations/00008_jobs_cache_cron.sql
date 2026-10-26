-- +goose Up
ALTER TABLE agent_jobs_cache
  ADD COLUMN cron TEXT NOT NULL DEFAULT '',
  ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE agent_jobs_cache
  DROP COLUMN cron,
  DROP COLUMN enabled;
