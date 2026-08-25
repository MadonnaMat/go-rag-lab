// Package retrieve orchestrates answering a user query: embedding it with
// the same embedding.Provider used at ingestion time, then asking the store
// for the nearest chunks — wiring together the embedding and store packages
// without either of them knowing about each other, the query-side mirror of
// internal/ingest.
package retrieve

import (
	"context"
	"fmt"

	"github.com/MadonnaMat/go-rag-lab/internal/embedding"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// Store is the subset of *store.Store that retrieval needs. Defining it
// here (rather than depending on the concrete type) lets tests substitute a
// fake, in-memory implementation with no real Postgres involved — same
// reasoning as ingest.Store.
type Store interface {
	SearchChunks(ctx context.Context, queryEmbedding []float32, topK int) ([]store.SearchResult, error)
}

type Retriever struct {
	Store    Store
	Provider embedding.Provider
}

// Query embeds q with the same provider/model used at ingestion time, then
// returns the topK nearest chunks, nearest first.
func (r *Retriever) Query(ctx context.Context, q string, topK int) ([]store.SearchResult, error) {
	if q == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if topK <= 0 {
		return nil, fmt.Errorf("topK must be positive, got %d", topK)
	}

	embeddings, err := r.Provider.Embed(ctx, []string{q})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) != 1 {
		return nil, fmt.Errorf("embedding provider returned %d vectors for 1 query", len(embeddings))
	}

	results, err := r.Store.SearchChunks(ctx, embeddings[0], topK)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	return results, nil
}
