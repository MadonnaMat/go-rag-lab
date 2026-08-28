-- Pure pgvector cosine-similarity search: the topK chunks nearest the query
-- embedding ($1), nearest first. $2 is the limit.
SELECT d.path, c.chunk_index, c.content, c.embedding <=> $1 AS distance
FROM chunks c
JOIN documents d ON d.id = c.document_id
ORDER BY c.embedding <=> $1
LIMIT $2
