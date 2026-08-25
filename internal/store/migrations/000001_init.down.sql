-- Order matters: chunks references documents via a foreign key.
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS documents;

-- The vector extension is left in place: it's a cluster-level object that
-- may be shared by other databases/schemas, so "undo this migration"
-- doesn't mean "remove it."
