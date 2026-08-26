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
	"strings"

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
	UpsertCorpusSummary(ctx context.Context, summary string) error
	GetIngestDirHash(ctx context.Context) (string, error)
	SetIngestDirHash(ctx context.Context, hash string) error
}

// Summarizer generates a short description of sampled corpus content —
// satisfied by *chat.OllamaChat.Summarize, kept as a local interface here
// (rather than importing internal/chat) so ingestion doesn't depend on
// the chat package for anything but this one call shape.
type Summarizer interface {
	Summarize(ctx context.Context, sample string) (string, error)
}

// maxSummarySampleChars caps how much ingested text gets sent to the
// Summarizer in one call, keeping the corpus-summary request small
// regardless of how much was ingested.
const maxSummarySampleChars = 12000

type Ingester struct {
	Store    Store
	Provider embedding.Provider
	// Summarizer is optional; if nil, no corpus summary is generated (chat
	// still works fine without one — see internal/chat.Chatter.Summaries).
	Summarizer   Summarizer
	ChunkSize    int
	ChunkOverlap int
}

// Result summarizes one IngestDir run.
type Result struct {
	Documents int
	Chunks    int
	// Skipped is true when dir's content hash matched the last successful
	// run's — nothing was chunked, embedded, or summarized.
	Skipped bool
}

// IngestDir reads every regular file directly inside dir (non-recursive),
// chunks and embeds each one, and upserts it into the store. Documents are
// processed in a fixed (sorted-by-name) order — os.ReadDir already
// guarantees this — so output and error messages are reproducible between
// runs.
//
// Before doing any of that, it hashes dir's contents (filenames + bytes)
// and compares against the hash stored by the last successful run — if
// they match, it returns immediately (Result.Skipped = true) without
// touching the embedding provider or the store, so re-running ingestion
// against unchanged input doesn't redo expensive embedding/summarization
// work. Adding, removing, renaming, or modifying any file changes the
// hash and triggers a full re-ingest.
func (ing *Ingester) IngestDir(ctx context.Context, dir string) (Result, error) {
	names, files, err := readDirFiles(dir)
	if err != nil {
		return Result{}, err
	}

	newHash := dirHash(files)
	prevHash, err := ing.Store.GetIngestDirHash(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("get previous ingest hash: %w", err)
	}
	if prevHash != "" && prevHash == newHash {
		return Result{Skipped: true}, nil
	}

	result, sample, err := ing.ingestFiles(ctx, names, files)
	if err != nil {
		return result, err
	}

	if err := ing.maybeGenerateCorpusSummary(ctx, result, sample); err != nil {
		return result, err
	}

	if err := ing.Store.SetIngestDirHash(ctx, newHash); err != nil {
		return result, fmt.Errorf("store ingest dir hash: %w", err)
	}

	return result, nil
}

// readDirFiles lists every regular file directly inside dir (non-recursive)
// and reads its content, returning names in a fixed (sorted-by-name) order
// — os.ReadDir already guarantees this — so output/error messages and the
// resulting dirHash stay reproducible between runs.
func readDirFiles(dir string) ([]string, map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read directory %q: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}

	files := make(map[string][]byte, len(names))
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, fmt.Errorf("read %q: %w", name, err)
		}
		files[name] = content
	}
	return names, files, nil
}

// ingestFiles chunks, embeds, and stores each named file, and returns a
// sample of their content for maybeGenerateCorpusSummary.
func (ing *Ingester) ingestFiles(ctx context.Context, names []string, files map[string][]byte) (Result, string, error) {
	var result Result
	var sample strings.Builder
	for _, name := range names {
		n, err := ing.ingestFile(ctx, name, files[name])
		if err != nil {
			return result, "", fmt.Errorf("ingest %q: %w", name, err)
		}
		result.Documents++
		result.Chunks += n
		if sample.Len() < maxSummarySampleChars {
			sample.Write(files[name])
			sample.WriteString("\n\n---\n\n")
		}
	}
	return result, sample.String(), nil
}

// maybeGenerateCorpusSummary runs the Summarizer (if configured) over
// sample and stores the result. A no-op if Summarizer is nil or nothing
// was ingested.
func (ing *Ingester) maybeGenerateCorpusSummary(ctx context.Context, result Result, sample string) error {
	if ing.Summarizer == nil || result.Documents == 0 {
		return nil
	}
	if len(sample) > maxSummarySampleChars {
		sample = sample[:maxSummarySampleChars]
	}
	summary, err := ing.Summarizer.Summarize(ctx, sample)
	if err != nil {
		return fmt.Errorf("generate corpus summary: %w", err)
	}
	if err := ing.Store.UpsertCorpusSummary(ctx, strings.TrimSpace(summary)); err != nil {
		return fmt.Errorf("store corpus summary: %w", err)
	}
	return nil
}

// ingestFile stores content under identity, its filename alone rather
// than the full path — dir may be an absolute path, a relative one, or a
// container mount point that differs between environments (compare
// running natively vs. the Dockerfile's -dir=/app/sample_docs), and
// document identity must stay stable across all of them so re-ingesting
// the same file replaces its chunks instead of duplicating them under a
// second, differently-prefixed path.
//
// This is only correct because IngestDir is non-recursive: if it ever
// walks subdirectories, bare-filename identity must become a path relative
// to dir (e.g. via filepath.Rel(dir, diskPath)) instead — otherwise two
// same-named files in different subdirectories would silently collide
// under ON CONFLICT (path), one overwriting the other with no error.
func (ing *Ingester) ingestFile(ctx context.Context, identity string, content []byte) (int, error) {
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

	docID, err := ing.Store.UpsertDocument(ctx, identity, contentHash)
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
