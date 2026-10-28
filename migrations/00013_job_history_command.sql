-- +goose Up
-- Lets the Central enqueue a "job_history" command, answered by the agent
-- from its own execution history, for the dashboard's per-server timeline
-- chart (one bar per finished backup, colored by success/failure).
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'pause_job', 'resume_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job', 'get_job', 'job_history'));

-- +goose Down
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'pause_job', 'resume_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job', 'get_job'));
