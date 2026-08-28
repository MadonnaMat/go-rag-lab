-- Hybrid search: rank chunks by vector similarity and by full-text keyword
-- relevance independently, then fuse the two rankings with Reciprocal Rank
-- Fusion (RRF). A chunk that appears in only one ranking still scores from
-- that side (FULL OUTER JOIN + COALESCE).
--
-- $1 = query embedding, $2 = query text, $3 = limit, $4 = RRF constant k.
WITH vec AS (
    SELECT c.id,
           row_number() OVER (ORDER BY c.embedding <=> $1) AS rank,
           c.embedding <=> $1 AS distance
    FROM chunks c
    ORDER BY c.embedding <=> $1
    LIMIT $3
),
kw AS (
    SELECT c.id,
           row_number() OVER (
               ORDER BY ts_rank(c.content_tsv, websearch_to_tsquery('english', $2)) DESC
           ) AS rank
    FROM chunks c
    WHERE c.content_tsv @@ websearch_to_tsquery('english', $2)
    LIMIT $3
),
fused AS (
    SELECT COALESCE(vec.id, kw.id) AS id,
           COALESCE(1.0 / ($4 + vec.rank), 0)
             + COALESCE(1.0 / ($4 + kw.rank), 0) AS score,
           COALESCE(vec.distance, 0) AS distance
    FROM vec
    FULL OUTER JOIN kw ON kw.id = vec.id
)
SELECT d.path, c.chunk_index, c.content, f.distance, f.score
FROM fused f
JOIN chunks c ON c.id = f.id
JOIN documents d ON d.id = c.document_id
ORDER BY f.score DESC
LIMIT $3
