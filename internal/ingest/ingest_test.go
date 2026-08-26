package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	nextID        int64
	documents     map[string]int64 // path -> id
	chunksByDoc   map[int64][]store.Chunk
	corpusSummary string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		documents:   map[string]int64{},
		chunksByDoc: map[int64][]store.Chunk{},
	}
}

func (f *fakeStore) UpsertCorpusSummary(ctx context.Context, summary string) error {
	f.corpusSummary = summary
	return nil
}

// UpsertDocument mirrors store.Store's real ON CONFLICT (path) DO UPDATE
// ... RETURNING id behavior: a path already seen gets back its existing id,
// not a new one.
func (f *fakeStore) UpsertDocument(ctx context.Context, path, contentHash string) (int64, error) {
	if id, ok := f.documents[path]; ok {
		return id, nil
	}
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestIngestDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "0123456789ABCDEF") // 16 chars
	writeFile(t, dir, "b.md", "short")

	provider := &fakeProvider{}
	st := newFakeStore()
	ing := &Ingester{Store: st, Provider: provider, ChunkSize: 10, ChunkOverlap: 4}

	result, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)

	assert.Equal(t, 2, result.Documents)
	// a.md (16 chars, size 10, overlap 4) -> 2 chunks; b.md (short) -> 1 chunk.
	assert.Equal(t, 3, result.Chunks)

	require.Len(t, provider.calls, 2, "Embed should be called once per document, batched")
	assert.Len(t, provider.calls[0], 2, "first Embed call should get a.md's 2 chunks")

	// Stored under its filename alone, not the full disk path — see the
	// comment on ingestFile for why (the same file must resolve to the
	// same identity whether ingested natively or from a container mount).
	aID, ok := st.documents["a.md"]
	require.True(t, ok, "a.md was never upserted")
	aChunks := st.chunksByDoc[aID]
	require.Len(t, aChunks, 2)
	assert.Equal(t, "0123456789", aChunks[0].Content)
	assert.Equal(t, "6789ABCDEF", aChunks[1].Content)
	assert.Equal(t, 0, aChunks[0].Index)
	assert.Equal(t, 1, aChunks[1].Index)
}

func TestIngestDir_ReingestSamePathReusesDocumentID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "hello world")

	st := newFakeStore()
	ing := &Ingester{Store: st, Provider: &fakeProvider{}, ChunkSize: 100, ChunkOverlap: 0}

	_, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	firstID, ok := st.documents["doc.md"]
	require.True(t, ok, "doc.md was never upserted on first run")

	_, err = ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	secondID, ok := st.documents["doc.md"]
	require.True(t, ok, "doc.md was never upserted on second run")

	assert.Equal(t, firstID, secondID, "document id should stay stable across re-ingestion (matches ON CONFLICT ... RETURNING id)")
	assert.Len(t, st.documents, 1)
}

func TestIngestDir_SkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "hello world")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o755))
	writeFile(t, filepath.Join(dir, "subdir"), "nested.md", "should not be ingested")

	ing := &Ingester{Store: newFakeStore(), Provider: &fakeProvider{}, ChunkSize: 100, ChunkOverlap: 0}
	result, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Documents, "subdirectory should be skipped")
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
	_, err := ing.IngestDir(context.Background(), dir)
	require.Error(t, err)
}

// fakeSummarizer records the sample it was called with and returns a
// fixed summary, so tests never need a real Ollama chat server.
type fakeSummarizer struct {
	calls []string
}

func (f *fakeSummarizer) Summarize(ctx context.Context, sample string) (string, error) {
	f.calls = append(f.calls, sample)
	return "  a summary  ", nil
}

func TestIngestDir_GeneratesCorpusSummary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "hello world")

	st := newFakeStore()
	summarizer := &fakeSummarizer{}
	ing := &Ingester{Store: st, Provider: &fakeProvider{}, Summarizer: summarizer, ChunkSize: 100, ChunkOverlap: 0}

	_, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)

	require.Len(t, summarizer.calls, 1)
	assert.Contains(t, summarizer.calls[0], "hello world")
	assert.Equal(t, "a summary", st.corpusSummary, "should be trimmed before storing")
}

func TestIngestDir_NilSummarizerSkipsCorpusSummary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "hello world")

	st := newFakeStore()
	ing := &Ingester{Store: st, Provider: &fakeProvider{}, ChunkSize: 100, ChunkOverlap: 0}

	_, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, st.corpusSummary)
}

type erroringProvider struct{}

func (erroringProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, errEmbedFailed
}

var errEmbedFailed = errors.New("embedding backend unavailable")
