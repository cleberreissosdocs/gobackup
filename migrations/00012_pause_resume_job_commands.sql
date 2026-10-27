-- +goose Up
-- Lets the Central enqueue "pause_job" and "resume_job" commands the same
-- way "cancel_job" already was (allowed since 00001 but never actually
-- implemented until now). "cancel_job" needs no change here.
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'pause_job', 'resume_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job', 'get_job'));

-- +goose Down
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job', 'get_job'));
