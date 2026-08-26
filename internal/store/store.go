// Package store persists documents and their embedded chunks in Postgres +
// pgvector.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

type Store struct {
	pool *pgxpool.Pool
}

// Chunk is a single embedded chunk ready to be stored.
type Chunk struct {
	Index     int
	Content   string
	Embedding []float32
}

// Open connects to Postgres and registers pgvector's wire-format codec on
// every connection in the pool, so query args/results can use []float32
// (via pgvector.NewVector) directly.
//
// pgxvec.RegisterTypes looks up the vector type's OID, which only exists
// once the vector extension has been created — Open assumes that, and the
// rest of the schema, is already in place via `make migrate` /
// `internal/store.MigrateUp` (see migrate.go). Unlike the old EnsureSchema,
// there's no defensive "create it if missing" step here anymore: schema
// setup is now the migration runner's job alone, run once, explicitly,
// before the app ever calls Open.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

// UpsertDocument inserts a document row, or updates its content_hash if a
// row for that path already exists, returning the row's id either way.
func (s *Store) UpsertDocument(ctx context.Context, path, contentHash string) (int64, error) {
	const q = `
		INSERT INTO documents (path, content_hash)
		VALUES ($1, $2)
		ON CONFLICT (path) DO UPDATE SET content_hash = EXCLUDED.content_hash
		RETURNING id`

	var id int64
	if err := s.pool.QueryRow(ctx, q, path, contentHash).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert document %q: %w", path, err)
	}
	return id, nil
}

// DeleteDocument deletes a document row, cascading to its chunks via the
// chunks table's ON DELETE CASCADE foreign key.
func (s *Store) DeleteDocument(ctx context.Context, path string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM documents WHERE path = $1`, path); err != nil {
		return fmt.Errorf("delete document %q: %w", path, err)
	}
	return nil
}

// ReplaceChunks deletes any existing chunks for documentID and inserts the
// given ones in their place, in one transaction — so re-running ingestion
// on the same document replaces its chunks rather than duplicating them.
func (s *Store) ReplaceChunks(ctx context.Context, documentID int64, chunks []Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed below

	// DELETE is queued as the batch's first statement, alongside the
	// inserts, so it pipelines in the same round trip instead of waiting
	// on its own reply before the inserts are even sent.
	batch := &pgx.Batch{}
	batch.Queue(`DELETE FROM chunks WHERE document_id = $1`, documentID)
	const insert = `
		INSERT INTO chunks (document_id, chunk_index, content, embedding)
		VALUES ($1, $2, $3, $4)`
	for _, c := range chunks {
		batch.Queue(insert, documentID, c.Index, c.Content, pgvector.NewVector(c.Embedding))
	}

	results := tx.SendBatch(ctx, batch)
	if _, err := results.Exec(); err != nil {
		_ = results.Close()
		return fmt.Errorf("delete existing chunks for document %d: %w", documentID, err)
	}
	for range chunks {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("insert chunk for document %d: %w", documentID, err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("close batch replace for document %d: %w", documentID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit chunk replacement for document %d: %w", documentID, err)
	}
	return nil
}

// SearchResult is one chunk returned by a similarity search, together with
// enough metadata to be useful to a caller: which document it came from and
// how close a match it is.
type SearchResult struct {
	Source   string
	Content  string
	Distance float64
}

// SearchChunks returns the topK chunks whose embeddings are closest to
// queryEmbedding, nearest first, using pgvector's cosine-distance operator
// (<=>) — the metric nomic-embed-text (internal/config's default) is
// designed for, and the same one migration 000002's HNSW index is built
// against. Distance is 1 - cosine similarity: 0 means an exact match,
// larger means less similar.
func (s *Store) SearchChunks(ctx context.Context, queryEmbedding []float32, topK int) ([]SearchResult, error) {
	const q = `
		SELECT d.path, c.content, c.embedding <=> $1 AS distance
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		ORDER BY c.embedding <=> $1
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, pgvector.NewVector(queryEmbedding), topK)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Source, &r.Content, &r.Distance); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return results, nil
}

// UpsertCorpusSummary replaces the corpus_summary singleton row — called
// once per ingestion run (see internal/ingest), not per chat request.
func (s *Store) UpsertCorpusSummary(ctx context.Context, summary string) error {
	const q = `
		INSERT INTO corpus_summary (id, summary, updated_at)
		VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET summary = EXCLUDED.summary, updated_at = EXCLUDED.updated_at`

	if _, err := s.pool.Exec(ctx, q, summary); err != nil {
		return fmt.Errorf("upsert corpus summary: %w", err)
	}
	return nil
}

// GetCorpusSummary returns the corpus summary, or "" if ingestion hasn't
// produced one yet (no error in that case — chat works fine without it).
func (s *Store) GetCorpusSummary(ctx context.Context) (string, error) {
	var summary string
	err := s.pool.QueryRow(ctx, `SELECT summary FROM corpus_summary WHERE id = 1`).Scan(&summary)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get corpus summary: %w", err)
	}
	return summary, nil
}

// CountChunks returns the total number of chunk rows, for tests and
// smoke-testing ingestion.
func (s *Store) CountChunks(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count chunks: %w", err)
	}
	return n, nil
}

func (s *Store) Close() {
	s.pool.Close()
}
