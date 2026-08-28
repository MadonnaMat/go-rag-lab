DROP INDEX IF EXISTS chunks_content_tsv_idx;
ALTER TABLE chunks DROP COLUMN IF EXISTS content_tsv;
