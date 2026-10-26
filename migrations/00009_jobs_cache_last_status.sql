-- +goose Up
ALTER TABLE agent_jobs_cache
  ADD COLUMN last_status TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_jobs_cache
  DROP COLUMN last_status;
