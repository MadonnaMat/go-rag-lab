-- Singleton table: one row describing the whole ingested corpus, generated
-- by cmd/ingest (via internal/chat's Ollama chat client, not the embedding
-- provider) and re-upserted every ingestion run. internal/chat.Chatter
-- loads it per chat request and appends it after the static system
-- prompt, so the model gets a description of what's actually been
-- ingested without that being baked into the binary.
CREATE TABLE corpus_summary (
    id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    summary    TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
