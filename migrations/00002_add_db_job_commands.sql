-- +goose Up
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir', 'create_db_job', 'start_db_job'));

-- +goose Down
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir'));
