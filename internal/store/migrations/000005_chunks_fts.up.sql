-- Full-text search alongside the pgvector similarity search: a generated
-- tsvector column over chunks.content plus a GIN index for it. Generated
-- (STORED) so ingestion never has to compute or write it — every existing
-- and future row gets one for free — and internal/store.SearchChunks can
-- run a hybrid vector + keyword ranking (see its RRF query).
--
-- 'english' is hard-coded to match the corpus language; revisit alongside a
-- configurable embedding model if the corpus ever stops being English.
--
-- Plain CREATE INDEX, not CONCURRENTLY: golang-migrate wraps each file in a
-- transaction (same note as 000002's HNSW index).
ALTER TABLE chunks
    ADD COLUMN content_tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('english', content)) STORED;

CREATE INDEX chunks_content_tsv_idx ON chunks USING gin (content_tsv);
