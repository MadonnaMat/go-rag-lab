-- Insert one chunk. Queued once per chunk in ReplaceChunks' batch, after a
-- DELETE of the document's existing chunks.
-- $1 = document id, $2 = chunk index, $3 = content, $4 = embedding vector.
INSERT INTO chunks (document_id, chunk_index, content, embedding)
VALUES ($1, $2, $3, $4)
