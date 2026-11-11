-- Creates the claims database and its two login roles. Runs once, as the Postgres superuser:
--   * in Compose, from /docker-entrypoint-initdb.d when the data volume is empty;
--   * in CI, against a fresh Postgres service (see .github/workflows/ci.yml).
--
-- claims_owner owns the database and its schema; migrations and the seed run as it.
-- claims_app is what the services use. It starts with CONNECT only: each migration grants
-- exactly what the services need, and scripts/db-check.sh fails on any difference.
--
-- The passwords come from the CLAIMS_OWNER_PASSWORD and CLAIMS_APP_PASSWORD environment
-- variables, read with psql's \getenv. Plain SQL and psql commands, so no shell is needed.

\set ON_ERROR_STOP on

\getenv owner_password CLAIMS_OWNER_PASSWORD
\getenv app_password CLAIMS_APP_PASSWORD

\if :{?owner_password}
\else
    DO $$ BEGIN RAISE EXCEPTION 'CLAIMS_OWNER_PASSWORD is not set'; END $$;
\endif
\if :{?app_password}
\else
    DO $$ BEGIN RAISE EXCEPTION 'CLAIMS_APP_PASSWORD is not set'; END $$;
\endif

SELECT :'owner_password' = '' AS owner_password_empty,
       :'app_password' = '' AS app_password_empty
\gset
\if :owner_password_empty
    DO $$ BEGIN RAISE EXCEPTION 'CLAIMS_OWNER_PASSWORD is empty'; END $$;
\endif
\if :app_password_empty
    DO $$ BEGIN RAISE EXCEPTION 'CLAIMS_APP_PASSWORD is empty'; END $$;
\endif

CREATE ROLE claims_owner LOGIN PASSWORD :'owner_password';
CREATE ROLE claims_app LOGIN PASSWORD :'app_password';

-- Guard rails on the services' sessions. Migrations run as claims_owner, so long-running
-- DDL is not cut off.
ALTER ROLE claims_app SET statement_timeout = '10s';
ALTER ROLE claims_app SET idle_in_transaction_session_timeout = '30s';

CREATE DATABASE claims OWNER claims_owner;
ALTER DATABASE claims SET timezone TO 'UTC';
REVOKE ALL ON DATABASE claims FROM PUBLIC;
GRANT CONNECT ON DATABASE claims TO claims_app;
