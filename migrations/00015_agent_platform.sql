-- +goose Up
-- Platform reported by the agent on every heartbeat ("windows"/"linux").
-- Lets the job form offer or hide platform-specific options instead of
-- guessing -- e.g. network/UNC folders with SMB credentials, which only
-- the Windows agent implements. Empty until an agent on a new enough
-- build checks in.
ALTER TABLE agents ADD COLUMN platform TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agents DROP COLUMN platform;
