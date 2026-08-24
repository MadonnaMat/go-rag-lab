// Package store persists documents and their embedded chunks in Postgres +
// pgvector.
package store

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// schemaSQL is the DDL in schema.sql, embedded into the compiled binary at
// build time via go:embed so no separate SQL file needs to ship or be
// mounted alongside the app — the schema-setup code path is identical
// locally, in docker-compose, and in CI.
//
//go:embed schema.sql
var schemaSQL string

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
// This needs a two-step connect: pgxvec.RegisterTypes looks up the vector
// type's OID, which only exists once the extension has been created. On a
// brand-new database that hasn't happened yet, so a plain bootstrap
// connection creates the extension first, before any pooled connection
// (which registers types in AfterConnect below) is opened.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	bootstrap, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to create vector extension: %w", err)
	}
	_, err = bootstrap.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	bootstrap.Close(ctx)
	if err != nil {
		return nil, fmt.Errorf("create vector extension: %w", err)
	}

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

// EnsureSchema creates the vector extension and tables if they don't
// already exist. Safe to call every time the app starts.
func (s *Store) EnsureSchema(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	return nil
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

// ReplaceChunks deletes any existing chunks for documentID and inserts the
// given ones in their place, in one transaction — so re-running ingestion
// on the same document replaces its chunks rather than duplicating them.
func (s *Store) ReplaceChunks(ctx context.Context, documentID int64, chunks []Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed below

	if _, err := tx.Exec(ctx, `DELETE FROM chunks WHERE document_id = $1`, documentID); err != nil {
		return fmt.Errorf("delete existing chunks for document %d: %w", documentID, err)
	}

	batch := &pgx.Batch{}
	const insert = `
		INSERT INTO chunks (document_id, chunk_index, content, embedding)
		VALUES ($1, $2, $3, $4)`
	for _, c := range chunks {
		batch.Queue(insert, documentID, c.Index, c.Content, pgvector.NewVector(c.Embedding))
	}

	if batch.Len() > 0 {
		results := tx.SendBatch(ctx, batch)
		for range chunks {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return fmt.Errorf("insert chunk for document %d: %w", documentID, err)
			}
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("close batch insert for document %d: %w", documentID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit chunk replacement for document %d: %w", documentID, err)
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
