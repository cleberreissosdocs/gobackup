-- +goose Up
ALTER TABLE backup_jobs
  ADD COLUMN dest_path TEXT,
  ADD COLUMN dest_type TEXT,
  ADD COLUMN link_dest TEXT,
  ADD COLUMN ssh_host TEXT,
  ADD COLUMN ssh_port INTEGER,
  ADD COLUMN ssh_user TEXT,
  ADD COLUMN ssh_key_path TEXT;

ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir', 'create_db_job', 'start_db_job', 'list_jobs', 'run_job'));

-- +goose Down
ALTER TABLE agent_commands DROP CONSTRAINT agent_commands_type_check;
ALTER TABLE agent_commands ADD CONSTRAINT agent_commands_type_check
    CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir', 'create_db_job', 'start_db_job'));

ALTER TABLE backup_jobs
  DROP COLUMN dest_path,
  DROP COLUMN dest_type,
  DROP COLUMN link_dest,
  DROP COLUMN ssh_host,
  DROP COLUMN ssh_port,
  DROP COLUMN ssh_user,
  DROP COLUMN ssh_key_path;
