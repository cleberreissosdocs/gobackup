-- +goose Up
-- Timezone the agent runs in, reported on every heartbeat.
--
-- A cron expression is evaluated by the AGENT, in the agent's own clock --
-- not in the browser's and not in the Central's. On a box whose host runs
-- UTC while the agent container runs America/Sao_Paulo, a schedule typed
-- as "03:00" fires three hours away from what the operator meant, and the
-- only symptom is a backup that silently never runs at the expected time.
-- Showing the agent's timezone next to the schedule field is what makes
-- that difference visible before the job is saved.
--
-- Two columns on purpose:
--   timezone          -- IANA name ("America/Sao_Paulo") when the agent can
--                        name it, else an abbreviation ("-03"). Preferred,
--                        because the offset can then be recomputed at render
--                        time and stays right across daylight-saving changes.
--   tz_offset_minutes -- offset the agent last reported, used only when the
--                        name cannot be resolved on this machine (missing
--                        tzdata, or an abbreviation we cannot look up).
--
-- Both stay empty/zero until an agent on a new enough build checks in;
-- callers must treat that as "unknown" and say so, never as UTC.
--
-- Written with IF NOT EXISTS because migrations now run on every serve
-- startup and must be safe to re-apply.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS tz_offset_minutes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE agents DROP COLUMN IF EXISTS timezone;
ALTER TABLE agents DROP COLUMN IF EXISTS tz_offset_minutes;
