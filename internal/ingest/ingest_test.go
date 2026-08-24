package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// fakeProvider returns one fixed-length embedding per input text, so tests
// never need a real Ollama server.
type fakeProvider struct {
	calls [][]string // records each Embed call's input, in order
}

func (f *fakeProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls = append(f.calls, texts)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i)}
	}
	return out, nil
}

// fakeStore records what would have been persisted, so tests never need a
// real Postgres.
type fakeStore struct {
	nextID      int64
	documents   map[string]int64 // path -> id
	chunksByDoc map[int64][]store.Chunk
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		documents:   map[string]int64{},
		chunksByDoc: map[int64][]store.Chunk{},
	}
}

func (f *fakeStore) UpsertDocument(ctx context.Context, path, contentHash string) (int64, error) {
	f.nextID++
	f.documents[path] = f.nextID
	return f.nextID, nil
}

func (f *fakeStore) ReplaceChunks(ctx context.Context, documentID int64, chunks []store.Chunk) error {
	f.chunksByDoc[documentID] = chunks
	return nil
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestIngestDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "0123456789ABCDEF") // 16 chars
	writeFile(t, dir, "b.md", "short")

	provider := &fakeProvider{}
	st := newFakeStore()
	ing := &Ingester{Store: st, Provider: provider, ChunkSize: 10, ChunkOverlap: 4}

	result, err := ing.IngestDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("IngestDir returned error: %v", err)
	}

	if result.Documents != 2 {
		t.Errorf("Documents = %d, want 2", result.Documents)
	}
	// a.md (16 chars, size 10, overlap 4) -> 2 chunks; b.md (short) -> 1 chunk.
	if result.Chunks != 3 {
		t.Errorf("Chunks = %d, want 3", result.Chunks)
	}

	if len(provider.calls) != 2 {
		t.Fatalf("Embed called %d times, want 2 (once per document, batched)", len(provider.calls))
	}
	if len(provider.calls[0]) != 2 {
		t.Errorf("first Embed call got %d texts, want 2 (a.md's chunks)", len(provider.calls[0]))
	}

	// Stored under its filename alone, not the full disk path — see the
	// comment on ingestFile for why (the same file must resolve to the
	// same identity whether ingested natively or from a container mount).
	aID, ok := st.documents["a.md"]
	if !ok {
		t.Fatal("a.md was never upserted")
	}
	aChunks := st.chunksByDoc[aID]
	if len(aChunks) != 2 {
		t.Fatalf("a.md got %d stored chunks, want 2", len(aChunks))
	}
	if aChunks[0].Content != "0123456789" || aChunks[1].Content != "6789ABCDEF" {
		t.Errorf("a.md chunk contents = %q, %q, want %q, %q",
			aChunks[0].Content, aChunks[1].Content, "0123456789", "6789ABCDEF")
	}
	if aChunks[0].Index != 0 || aChunks[1].Index != 1 {
		t.Errorf("a.md chunk indexes = %d, %d, want 0, 1", aChunks[0].Index, aChunks[1].Index)
	}
}

func TestIngestDir_SkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "hello world")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "subdir"), "nested.md", "should not be ingested")

	ing := &Ingester{Store: newFakeStore(), Provider: &fakeProvider{}, ChunkSize: 100, ChunkOverlap: 0}
	result, err := ing.IngestDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("IngestDir returned error: %v", err)
	}
	if result.Documents != 1 {
		t.Errorf("Documents = %d, want 1 (subdirectory should be skipped)", result.Documents)
	}
}

func TestIngestDir_PropagatesProviderError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "hello world")

	ing := &Ingester{
		Store:        newFakeStore(),
		Provider:     erroringProvider{},
		ChunkSize:    100,
		ChunkOverlap: 0,
	}
	if _, err := ing.IngestDir(context.Background(), dir); err == nil {
		t.Fatal("IngestDir returned nil error, want the provider's error to propagate")
	}
}

type erroringProvider struct{}

func (erroringProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, errEmbedFailed
}

var errEmbedFailed = errors.New("embedding backend unavailable")
