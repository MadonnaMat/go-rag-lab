-- Singleton table tracking the directory-level content hash of the last
-- successful ingestion run (see internal/ingest's dirHash), so re-running
-- `make ingest`/`docker compose up app` against an unchanged lore_docs/
-- can skip re-chunking/re-embedding/re-summarizing entirely rather than
-- redoing expensive Ollama work for identical input. Separate from
-- corpus_summary (000003) since dir-hash tracking is unconditional —
-- corpus_summary generation is optional (only runs if an
-- ingest.Summarizer is configured).
CREATE TABLE ingest_state (
    id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    dir_hash   TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
