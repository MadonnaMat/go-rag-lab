-- Pure Postgres full-text search over the generated content_tsv column:
-- chunks matching websearch_to_tsquery of the query text ($1), ranked by
-- ts_rank, best first. $2 is the limit.
SELECT d.path, c.chunk_index, c.content,
       ts_rank(c.content_tsv, websearch_to_tsquery('english', $1)) AS score
FROM chunks c
JOIN documents d ON d.id = c.document_id
WHERE c.content_tsv @@ websearch_to_tsquery('english', $1)
ORDER BY score DESC
LIMIT $2
