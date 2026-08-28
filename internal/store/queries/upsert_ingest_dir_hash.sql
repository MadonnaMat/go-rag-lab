-- Replace the ingest_state singleton row (id = 1). $1 = directory hash.
INSERT INTO ingest_state (id, dir_hash, updated_at)
VALUES (1, $1, now())
ON CONFLICT (id) DO UPDATE SET dir_hash = EXCLUDED.dir_hash, updated_at = EXCLUDED.updated_at
