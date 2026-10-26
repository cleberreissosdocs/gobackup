-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    hostname TEXT NOT NULL,
    api_token_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('online', 'offline', 'unknown')),
    last_seen_at TIMESTAMPTZ,
    agent_version TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE destinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('local', 'nas', 'ssh')),
    path TEXT,
    nas_config JSONB,
    ssh_config JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE db_connections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    engine TEXT NOT NULL CHECK (engine IN ('mariadb', 'mysql', 'postgresql', 'sqlite')),
    host TEXT,
    port INTEGER,
    username TEXT,
    password_ref TEXT,
    database_name TEXT,
    sqlite_path TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE backup_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    job_kind TEXT NOT NULL CHECK (job_kind IN ('files', 'database')),

    -- job_kind = files
    source_path TEXT,
    destination_id UUID REFERENCES destinations(id),
    backup_type TEXT CHECK (backup_type IN ('full', 'incremental', 'differential')),

    -- job_kind = database
    db_connection_id UUID REFERENCES db_connections(id),
    dump_destination_id UUID REFERENCES destinations(id),
    compression TEXT CHECK (compression IN ('gzip', 'zip', 'none')),

    schedule_cron TEXT NOT NULL,
    retention_policy JSONB,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE job_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES backup_jobs(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'success', 'failed', 'cancelled')),
    trigger TEXT NOT NULL CHECK (trigger IN ('scheduled', 'manual', 'remote_central')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    bytes_transferred BIGINT,
    files_transferred BIGINT,
    error_message TEXT,
    log_ref TEXT
);

CREATE TABLE agent_commands (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('create_job', 'update_job', 'delete_job', 'start_job', 'cancel_job', 'list_dir')),
    payload JSONB,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'acked', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ,
    acked_at TIMESTAMPTZ,
    result JSONB
);

CREATE TABLE notification_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('email', 'telegram')),
    config JSONB NOT NULL,
    events JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agents_tenant ON agents(tenant_id);
CREATE INDEX idx_backup_jobs_agent ON backup_jobs(agent_id);
CREATE INDEX idx_job_executions_job ON job_executions(job_id);
CREATE INDEX idx_agent_commands_agent_status ON agent_commands(agent_id, status);

-- +goose Down
DROP TABLE IF EXISTS notification_channels;
DROP TABLE IF EXISTS agent_commands;
DROP TABLE IF EXISTS job_executions;
DROP TABLE IF EXISTS backup_jobs;
DROP TABLE IF EXISTS db_connections;
DROP TABLE IF EXISTS destinations;
DROP TABLE IF EXISTS agents;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
