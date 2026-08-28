-- Every ingested document with its chunk count, ordered by path. Used by the
-- chat list_resources tool.
SELECT d.path, count(c.id)
FROM documents d
LEFT JOIN chunks c ON c.document_id = d.id
GROUP BY d.path
ORDER BY d.path
