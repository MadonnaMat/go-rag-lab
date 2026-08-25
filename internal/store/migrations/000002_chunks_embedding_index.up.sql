-- HNSW index over chunks.embedding using pgvector's cosine-distance
-- operator class, matching the <=> operator internal/store.SearchChunks
-- queries with. HNSW (not ivfflat) needs no "expected row count" tuning
-- parameter, which suits a table whose size isn't known ahead of time.
--
-- Not CREATE INDEX CONCURRENTLY: golang-migrate wraps each migration file
-- in a transaction, and CONCURRENTLY cannot run inside one. Fine at this
-- table's lab scale (a short exclusive lock on a small table); revisit with
-- a dedicated non-transactional migration if this ever needs to run against
-- a large, live table.
CREATE INDEX chunks_embedding_hnsw_idx
    ON chunks USING hnsw (embedding vector_cosine_ops);
