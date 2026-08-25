// Command ingest reads every document in a directory, chunks and embeds
// each one, and stores the results in Postgres+pgvector. It's a thin
// wrapper: all the real logic lives in internal/ingest, internal/embedding,
// and internal/store — this file just parses flags and wires them
// together.
//
// Requires the database schema to already exist — run `make migrate` (or
// `docker compose run --rm migrate`) first. Unlike the old EnsureSchema,
// ingest no longer creates it for you: schema setup is the migration
// runner's job alone.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/MadonnaMat/go-rag-lab/internal/config"
	"github.com/MadonnaMat/go-rag-lab/internal/embedding"
	"github.com/MadonnaMat/go-rag-lab/internal/ingest"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

func main() {
	dir := flag.String("dir", "sample_docs", "directory of documents to ingest (non-recursive)")
	flag.Parse()

	if err := run(*dir); err != nil {
		log.Fatal(err)
	}
}

func run(dir string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx := context.Background()

	provider, err := embedding.New(cfg.EmbeddingProvider, cfg.OllamaURL, cfg.OllamaEmbedModel)
	if err != nil {
		return fmt.Errorf("build embedding provider: %w", err)
	}

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ing := &ingest.Ingester{
		Store:        s,
		Provider:     provider,
		ChunkSize:    cfg.ChunkSize,
		ChunkOverlap: cfg.ChunkOverlap,
	}

	result, err := ing.IngestDir(ctx, dir)
	if err != nil {
		return fmt.Errorf("ingest %q: %w", dir, err)
	}

	fmt.Printf("Documents: %d, Chunks: %d\n", result.Documents, result.Chunks)
	return nil
}
