// Package ingest orchestrates reading documents off disk, chunking them,
// embedding each chunk, and persisting the results — wiring together the
// chunk, embedding, and store packages without any of them knowing about
// each other.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/MadonnaMat/go-rag-lab/internal/chunk"
	"github.com/MadonnaMat/go-rag-lab/internal/embedding"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// Store is the subset of *store.Store that ingestion needs. Defining it
// here (rather than depending on the concrete type) lets tests substitute
// a fake, in-memory implementation with no real Postgres involved.
type Store interface {
	UpsertDocument(ctx context.Context, path, contentHash string) (int64, error)
	ReplaceChunks(ctx context.Context, documentID int64, chunks []store.Chunk) error
}

type Ingester struct {
	Store        Store
	Provider     embedding.Provider
	ChunkSize    int
	ChunkOverlap int
}

// Result summarizes one IngestDir run.
type Result struct {
	Documents int
	Chunks    int
}

// IngestDir reads every regular file directly inside dir (non-recursive),
// chunks and embeds each one, and upserts it into the store. Documents are
// processed in a fixed (sorted-by-name) order so output and error messages
// are reproducible between runs.
func (ing *Ingester) IngestDir(ctx context.Context, dir string) (Result, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, fmt.Errorf("read directory %q: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var result Result
	for _, name := range names {
		path := filepath.Join(dir, name)
		n, err := ing.ingestFile(ctx, path)
		if err != nil {
			return result, fmt.Errorf("ingest %q: %w", path, err)
		}
		result.Documents++
		result.Chunks += n
	}
	return result, nil
}

func (ing *Ingester) ingestFile(ctx context.Context, path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read file: %w", err)
	}

	sum := sha256.Sum256(content)
	contentHash := hex.EncodeToString(sum[:])

	chunks, err := chunk.Split(string(content), ing.ChunkSize, ing.ChunkOverlap)
	if err != nil {
		return 0, fmt.Errorf("split into chunks: %w", err)
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	embeddings, err := ing.Provider.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed %d chunks: %w", len(texts), err)
	}
	if len(embeddings) != len(chunks) {
		return 0, fmt.Errorf("embedding provider returned %d vectors for %d chunks", len(embeddings), len(chunks))
	}

	docID, err := ing.Store.UpsertDocument(ctx, path, contentHash)
	if err != nil {
		return 0, fmt.Errorf("upsert document: %w", err)
	}

	storeChunks := make([]store.Chunk, len(chunks))
	for i, c := range chunks {
		storeChunks[i] = store.Chunk{Index: c.Index, Content: c.Text, Embedding: embeddings[i]}
	}
	if err := ing.Store.ReplaceChunks(ctx, docID, storeChunks); err != nil {
		return 0, fmt.Errorf("replace chunks: %w", err)
	}

	return len(chunks), nil
}
