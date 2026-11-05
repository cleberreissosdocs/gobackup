-- +goose Up
-- The mssql engine (SQL Server / SQL Server Express) was added to
-- core.DBEngine when SQL Server support shipped, but this table's CHECK
-- constraint was never extended to match -- every attempt to create a
-- database job with engine=mssql against this table (the Linux agent's
-- path; the Windows agent stores its jobs in its own local jobs.json and
-- never touches this table) fails the INSERT with a check-constraint
-- violation. Before the accompanying Go fix, that violation was then
-- silently swallowed by a fallback lookup and surfaced to the user as the
-- unhelpful "create db connection: no rows in result set" instead of
-- naming the real problem.
ALTER TABLE db_connections DROP CONSTRAINT db_connections_engine_check;
ALTER TABLE db_connections ADD CONSTRAINT db_connections_engine_check
    CHECK (engine IN ('mariadb', 'mysql', 'postgresql', 'sqlite', 'mssql'));

-- +goose Down
ALTER TABLE db_connections DROP CONSTRAINT db_connections_engine_check;
ALTER TABLE db_connections ADD CONSTRAINT db_connections_engine_check
    CHECK (engine IN ('mariadb', 'mysql', 'postgresql', 'sqlite'));
