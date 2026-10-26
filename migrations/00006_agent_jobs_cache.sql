-- +goose Up
-- Cache of each agent's job list, refreshed on every heartbeat (piggybacked
-- on the poll, no extra round trip) — lets the Central's dashboard show a
-- combined "Tarefas de todos os servidores" table without blocking on a
-- live remote command per agent on every page load.
CREATE TABLE agent_jobs_cache (
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, job_id)
);

-- +goose Down
DROP TABLE agent_jobs_cache;
