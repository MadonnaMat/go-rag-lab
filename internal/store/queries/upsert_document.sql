-- Insert a document, or update its content_hash if a row for that path
-- already exists; return the row id either way. $1 = path, $2 = content hash.
INSERT INTO documents (path, content_hash)
VALUES ($1, $2)
ON CONFLICT (path) DO UPDATE SET content_hash = EXCLUDED.content_hash
RETURNING id
