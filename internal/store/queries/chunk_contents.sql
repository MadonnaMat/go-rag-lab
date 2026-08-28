-- The exact ingested text of specific chunks of one document, keyed by
-- chunk index. Used by internal/api's /lore endpoint to locate the cited
-- passages in the rendered markdown without re-deriving them from the
-- current on-disk file. $1 = document path (bare filename), $2 = int[] of
-- chunk indices.
SELECT c.chunk_index, c.content
FROM chunks c
JOIN documents d ON d.id = c.document_id
WHERE d.path = $1 AND c.chunk_index = ANY($2)
