-- Replace the corpus_summary singleton row (id = 1). $1 = summary text.
INSERT INTO corpus_summary (id, summary, updated_at)
VALUES (1, $1, now())
ON CONFLICT (id) DO UPDATE SET summary = EXCLUDED.summary, updated_at = EXCLUDED.updated_at
