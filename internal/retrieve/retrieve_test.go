package retrieve

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// fakeProvider returns one fixed embedding per input text, recording what
// it was asked to embed — same pattern as internal/ingest's fakeProvider.
type fakeProvider struct {
	calls [][]string
	err   error
}

func (f *fakeProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls = append(f.calls, texts)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i)}
	}
	return out, nil
}

// fakeStore records the embedding/topK it was searched with and returns a
// fixed set of results — no real Postgres involved.
type fakeStore struct {
	gotEmbedding []float32
	gotTopK      int
	results      []store.SearchResult
	err          error
}

func (f *fakeStore) SearchChunks(ctx context.Context, queryEmbedding []float32, topK int) ([]store.SearchResult, error) {
	f.gotEmbedding = queryEmbedding
	f.gotTopK = topK
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func TestQuery_EmbedsAndSearches(t *testing.T) {
	provider := &fakeProvider{}
	want := []store.SearchResult{{Source: "a.md", Content: "hello", Distance: 0.1}}
	st := &fakeStore{results: want}
	r := &Retriever{Store: st, Provider: provider}

	got, err := r.Query(context.Background(), "how does X work", 3)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	require.Len(t, provider.calls, 1)
	assert.Equal(t, []string{"how does X work"}, provider.calls[0], "the query text should be embedded, batch of one")

	assert.Equal(t, []float32{0}, st.gotEmbedding, "the embedding returned by the provider should be passed straight through to SearchChunks")
	assert.Equal(t, 3, st.gotTopK)
}

func TestQuery_RejectsEmptyQuery(t *testing.T) {
	provider := &fakeProvider{}
	r := &Retriever{Store: &fakeStore{}, Provider: provider}

	_, err := r.Query(context.Background(), "", 3)
	require.Error(t, err)
	assert.Empty(t, provider.calls, "an empty query should be rejected before ever calling the embedding provider")
}

func TestQuery_RejectsNonPositiveTopK(t *testing.T) {
	provider := &fakeProvider{}
	r := &Retriever{Store: &fakeStore{}, Provider: provider}

	_, err := r.Query(context.Background(), "a query", 0)
	require.Error(t, err)
	assert.Empty(t, provider.calls, "a non-positive topK should be rejected before ever calling the embedding provider")
}

func TestQuery_PropagatesProviderError(t *testing.T) {
	errEmbedFailed := errors.New("embedding backend unavailable")
	r := &Retriever{Store: &fakeStore{}, Provider: &fakeProvider{err: errEmbedFailed}}

	_, err := r.Query(context.Background(), "a query", 3)
	require.Error(t, err)
	assert.ErrorIs(t, err, errEmbedFailed)
}

func TestQuery_PropagatesStoreError(t *testing.T) {
	errSearchFailed := errors.New("database unavailable")
	r := &Retriever{Store: &fakeStore{err: errSearchFailed}, Provider: &fakeProvider{}}

	_, err := r.Query(context.Background(), "a query", 3)
	require.Error(t, err)
	assert.ErrorIs(t, err, errSearchFailed)
}
