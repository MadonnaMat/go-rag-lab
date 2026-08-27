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
	dirHash       string
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

func (f *fakeStore) GetIngestDirHash(ctx context.Context) (string, error) {
	return f.dirHash, nil
}

func (f *fakeStore) SetIngestDirHash(ctx context.Context, hash string) error {
	f.dirHash = hash
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

func (f *fakeStore) ListDocuments(context.Context) ([]store.DocumentInfo, error) {
	out := make([]store.DocumentInfo, 0, len(f.documents))
	for path, id := range f.documents {
		out = append(out, store.DocumentInfo{Path: path, Chunks: len(f.chunksByDoc[id])})
	}
	return out, nil
}

func (f *fakeStore) DeleteDocument(_ context.Context, path string) error {
	if id, ok := f.documents[path]; ok {
		delete(f.chunksByDoc, id)
		delete(f.documents, path)
	}
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

	// Change the file's content (not just re-run unchanged) so the
	// dir-hash skip (see TestIngestDir_SkipsWhenDirUnchanged below)
	// doesn't short-circuit this run before it can exercise ON CONFLICT.
	writeFile(t, dir, "doc.md", "hello world, again")

	result, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	require.False(t, result.Skipped)
	secondID, ok := st.documents["doc.md"]
	require.True(t, ok, "doc.md was never upserted on second run")

	assert.Equal(t, firstID, secondID, "document id should stay stable across re-ingestion (matches ON CONFLICT ... RETURNING id)")
	assert.Len(t, st.documents, 1)
}

func TestIngestDir_SkipsWhenDirUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "hello world")

	provider := &fakeProvider{}
	st := newFakeStore()
	ing := &Ingester{Store: st, Provider: provider, ChunkSize: 100, ChunkOverlap: 0}

	result, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.Len(t, provider.calls, 1, "first run should embed")

	result, err = ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	assert.True(t, result.Skipped, "second run against unchanged content should be skipped")
	assert.Len(t, provider.calls, 1, "no new Embed calls on a skipped run")
}

func TestIngestDir_ReingestsWhenFileAddedRemovedOrModified(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{
			name: "file added",
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "b.md", "a new document")
			},
		},
		{
			name: "file modified",
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.md", "hello world, modified")
			},
		},
		{
			name: "file removed",
			mutate: func(t *testing.T, dir string) {
				require.NoError(t, os.Remove(filepath.Join(dir, "a.md")))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "a.md", "hello world")

			ing := &Ingester{Store: newFakeStore(), Provider: &fakeProvider{}, ChunkSize: 100, ChunkOverlap: 0}
			_, err := ing.IngestDir(context.Background(), dir)
			require.NoError(t, err)

			tt.mutate(t, dir)

			result, err := ing.IngestDir(context.Background(), dir)
			require.NoError(t, err)
			assert.False(t, result.Skipped)
		})
	}
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

func TestIngestFile(t *testing.T) {
	provider := &fakeProvider{}
	st := newFakeStore()
	st.dirHash = "stale-hash" // a prior IngestDir run left this

	ing := &Ingester{Store: st, Provider: provider, ChunkSize: 8, ChunkOverlap: 2}

	n, err := ing.IngestFile(context.Background(), "06-ulmarin-cuisine.md", []byte("The ulmarin eat moss and lichen."))
	require.NoError(t, err)
	assert.Positive(t, n)

	// Document upserted under its bare filename.
	id, ok := st.documents["06-ulmarin-cuisine.md"]
	require.True(t, ok)
	assert.Len(t, st.chunksByDoc[id], n)

	// Dir hash cleared so the next IngestDir does a full re-ingest.
	assert.Empty(t, st.dirHash)

	// Re-running with new content replaces chunks under the same id.
	n2, err := ing.IngestFile(context.Background(), "06-ulmarin-cuisine.md", []byte("Revised."))
	require.NoError(t, err)
	assert.Equal(t, id, st.documents["06-ulmarin-cuisine.md"])
	assert.Len(t, st.chunksByDoc[id], n2)
}

func TestIngestDir_DeletesOrphanedDocuments(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keep.md", "kept content")
	writeFile(t, dir, "gone.md", "doomed content")

	st := newFakeStore()
	ing := &Ingester{Store: st, Provider: &fakeProvider{}, ChunkSize: 100, ChunkOverlap: 0}

	r, err := ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Documents)
	assert.Equal(t, 0, r.Deleted)

	// Remove one file and re-ingest.
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.md")))
	r, err = ing.IngestDir(context.Background(), dir)
	require.NoError(t, err)

	assert.Equal(t, 1, r.Deleted)
	_, stillThere := st.documents["gone.md"]
	assert.False(t, stillThere, "orphaned document row should be deleted")
	assert.Len(t, st.chunksByDoc, 1, "orphaned document's chunks should be gone too")
	_, kept := st.documents["keep.md"]
	assert.True(t, kept)
}
