-- +goose Up
-- Persist the database password alongside the connection so a scheduled
-- backup can authenticate. Until now the password only ever arrived from
-- the "Executar" form, which meant the scheduler ran database jobs with
-- an empty password and they failed against any engine that requires
-- authentication.
--
-- The value is encrypted (AES-256-GCM) with a key generated on first use
-- and kept in app_secrets, i.e. in this same database. That protects a
-- stray dump or a stolen disk, NOT someone who can already query the
-- database -- for them the key sits one table away. This tradeoff was
-- chosen deliberately over a key supplied per installation, so that
-- installing an agent stays a single command with nothing to configure.
ALTER TABLE db_connections ADD COLUMN password_enc BYTEA;

-- password_ref was declared in the original schema and never written with
-- anything but an empty string; its Go field was dead code. Dropping it
-- so there are not two password-shaped columns inviting the next reader
-- to use the wrong one.
ALTER TABLE db_connections DROP COLUMN IF EXISTS password_ref;

CREATE TABLE app_secrets (
    name       TEXT PRIMARY KEY,
    value      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE app_secrets;
ALTER TABLE db_connections DROP COLUMN password_enc;
ALTER TABLE db_connections ADD COLUMN password_ref TEXT;
