-- +goose Up
-- Cache of each agent's currently-running (or just-finished) job
-- execution, refreshed on every heartbeat (same piggyback mechanism as
-- agent_jobs_cache) — lets the Central show a live-ish progress bar for a
-- remote job without blocking on a round trip.
CREATE TABLE agent_execution_status (
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL,
    status TEXT NOT NULL,
    percent INTEGER NOT NULL DEFAULT -1,
    message TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, job_id)
);

ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job', 'get_job'));

-- +goose Down
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job'));
DROP TABLE agent_execution_status;
