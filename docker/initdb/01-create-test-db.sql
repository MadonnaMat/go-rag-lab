-- Runs once, automatically, only the first time the db container's data
-- volume is initialized (see docker-compose.yml's db.volumes mount of this
-- directory into /docker-entrypoint-initdb.d). Gives tests their own
-- database, separate from the one `make ingest`/dev data lives in, so
-- `make test` never shares state with real ingested documents.
--
-- Hardcoded to match this project's default POSTGRES_DB (rag) — if you
-- change POSTGRES_DB in .env, update this file's database name to match.
CREATE DATABASE rag_test;
