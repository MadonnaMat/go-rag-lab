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

	"github.com/MadonnaMat/go-rag-lab/internal/store/queries"
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
	var id int64
	if err := s.pool.QueryRow(ctx, queries.UpsertDocument, path, contentHash).Scan(&id); err != nil {
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
	for _, c := range chunks {
		batch.Queue(queries.InsertChunk, documentID, c.Index, c.Content, pgvector.NewVector(c.Embedding))
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

// SearchResult is one chunk returned by a search, together with enough
// metadata to be useful to a caller: which document it came from, its index
// within that document (so a caller can re-locate the exact passage — see
// internal/api's /lore endpoint), and how good a match it is.
type SearchResult struct {
	Source     string
	ChunkIndex int
	Content    string
	// Distance is pgvector cosine distance (1 - cosine similarity): 0 means
	// an exact match, larger means less similar. It's 0 for a keyword-only
	// hit that never ranked on the vector side.
	Distance float64
	// Score is the Reciprocal Rank Fusion score for SearchAuto (higher is
	// better); 0 for the single-signal modes.
	Score float64
}

// SearchMode selects how SearchChunks ranks chunks.
type SearchMode string

const (
	// SearchAuto fuses vector similarity and full-text keyword ranking with
	// Reciprocal Rank Fusion — the default, and what a bare query wants.
	SearchAuto SearchMode = "auto"
	// SearchVector is pure pgvector cosine similarity.
	SearchVector SearchMode = "vector"
	// SearchKeyword is pure Postgres full-text search (ts_rank over the
	// generated content_tsv column).
	SearchKeyword SearchMode = "keyword"
)

// rrfK is the Reciprocal Rank Fusion constant passed to search_auto.sql as
// $4: a list's score contribution is 1/(rrfK + rank). 60 is the value from
// the original RRF paper and the common default — it damps the influence of
// the very top ranks just enough that a strong hit in one signal doesn't
// automatically dominate a moderate hit in both.
const rrfK = 60

// SearchChunks returns the topK best-matching chunks for a query, best
// first. queryEmbedding drives the vector side; queryText drives the
// full-text side (via websearch_to_tsquery, which never errors on arbitrary
// input). mode picks which signal(s) to use; an empty mode means SearchAuto.
func (s *Store) SearchChunks(ctx context.Context, queryEmbedding []float32, queryText string, mode SearchMode, topK int) ([]SearchResult, error) {
	switch mode {
	case "", SearchAuto:
		return scanSearch(ctx, s.pool, queries.SearchAuto, func(r *SearchResult) []any {
			return []any{&r.Source, &r.ChunkIndex, &r.Content, &r.Distance, &r.Score}
		}, pgvector.NewVector(queryEmbedding), queryText, topK, rrfK)
	case SearchVector:
		return scanSearch(ctx, s.pool, queries.SearchVector, func(r *SearchResult) []any {
			return []any{&r.Source, &r.ChunkIndex, &r.Content, &r.Distance}
		}, pgvector.NewVector(queryEmbedding), topK)
	case SearchKeyword:
		return scanSearch(ctx, s.pool, queries.SearchKeyword, func(r *SearchResult) []any {
			return []any{&r.Source, &r.ChunkIndex, &r.Content, &r.Score}
		}, queryText, topK)
	default:
		return nil, fmt.Errorf("unknown search mode %q", mode)
	}
}

// scanSearch runs a search query and scans each row into a SearchResult
// using the caller-supplied field list — the three search variants differ
// only in their SQL and which columns they select.
func scanSearch(ctx context.Context, pool *pgxpool.Pool, query string, dest func(*SearchResult) []any, args ...any) ([]SearchResult, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(dest(&r)...); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return results, nil
}

// DocumentInfo describes one ingested document: its identity (bare
// filename — see internal/ingest.ingestFile) and how many chunks it
// produced.
type DocumentInfo struct {
	Path   string
	Chunks int
}

// ListDocuments returns every ingested document with its chunk count,
// ordered by path — a pure read over the existing tables (no schema of its
// own). Used by the chat list_resources tool.
func (s *Store) ListDocuments(ctx context.Context) ([]DocumentInfo, error) {
	rows, err := s.pool.Query(ctx, queries.ListDocuments)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	var docs []DocumentInfo
	for rows.Next() {
		var d DocumentInfo
		if err := rows.Scan(&d.Path, &d.Chunks); err != nil {
			return nil, fmt.Errorf("scan document info: %w", err)
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate documents: %w", err)
	}
	return docs, nil
}

// UpsertCorpusSummary replaces the corpus_summary singleton row — called
// once per ingestion run (see internal/ingest), not per chat request.
func (s *Store) UpsertCorpusSummary(ctx context.Context, summary string) error {
	if _, err := s.pool.Exec(ctx, queries.UpsertCorpusSummary, summary); err != nil {
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

// GetIngestDirHash returns the directory-level content hash stored by the
// last successful ingestion run, or "" if there's never been one (a fresh
// database, or one that predates this check) — no error in that case, so
// callers can treat it as "always ingest."
func (s *Store) GetIngestDirHash(ctx context.Context) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT dir_hash FROM ingest_state WHERE id = 1`).Scan(&hash)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get ingest dir hash: %w", err)
	}
	return hash, nil
}

// SetIngestDirHash replaces the ingest_state singleton row — called once
// per successful ingestion run (see internal/ingest), after all
// documents/chunks/summary are written, not before.
func (s *Store) SetIngestDirHash(ctx context.Context, hash string) error {
	if _, err := s.pool.Exec(ctx, queries.UpsertIngestDirHash, hash); err != nil {
		return fmt.Errorf("set ingest dir hash: %w", err)
	}
	return nil
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
