package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/retrieve"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// integrationDatabaseURL returns DATABASE_URL, skipping the test entirely
// if it's unset — same convention as internal/store/internal/retrieve's
// DB-gated tests, so `go test ./...` still works with zero infra running.
func integrationDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping test that needs a real Postgres")
	}
	return url
}

// vec768 returns a 768-dim embedding with 1.0 at each given index and 0
// elsewhere — same construction as internal/store's test helper of the same
// name, duplicated here rather than shared since it's a few lines used in
// two packages.
func vec768(indices ...int) []float32 {
	v := make([]float32, 768)
	for _, i := range indices {
		v[i] = 1
	}
	return v
}

// fixedEmbedProvider always embeds to the same vector regardless of input
// text — the query text's actual content doesn't matter for this test,
// which is proving the HTTP-to-Postgres wiring, not embedding quality (that
// belongs to internal/embedding's own tests). No live Ollama needed.
type fixedEmbedProvider struct{ vector []float32 }

func (f fixedEmbedProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = f.vector
	}
	return out, nil
}

// TestQueryEndToEnd wires the real Store, real Retriever, and real
// NewRouter together behind a real HTTP listener (httptest.NewServer, an
// actual TCP connection — not ServeHTTP-against-a-Recorder like api_test.go's
// unit tests) and proves the whole chain works against a real Postgres
// running the real HNSW-indexed SearchChunks query. Only the embedding step
// is faked, so this needs DATABASE_URL but not a live Ollama.
func TestQueryEndToEnd(t *testing.T) {
	dbURL := integrationDatabaseURL(t)
	ctx := context.Background()

	s, err := store.Open(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	const path = "api_integration_test.go::query_e2e"
	t.Cleanup(func() {
		require.NoError(t, s.DeleteDocument(context.Background(), path))
	})

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	require.NoError(t, err)
	require.NoError(t, s.ReplaceChunks(ctx, docID, []store.Chunk{
		{Index: 0, Content: "exact match chunk", Embedding: vec768(0)},
		{Index: 1, Content: "orthogonal chunk", Embedding: vec768(1)},
	}))

	// go test ./... runs each package's tests as its own binary, and
	// multiple packages can run concurrently — internal/store's own
	// SearchChunks test could be inserting rows into this same shared
	// database at the same time this test queries it, and SearchChunks is
	// intentionally unscoped across the whole corpus. Rather than adding
	// artificial serialization, ask for every chunk in the table and filter
	// the response down to this test's own rows before asserting order.
	totalChunks, err := s.CountChunks(ctx)
	require.NoError(t, err)

	retriever := &retrieve.Retriever{Store: s, Provider: fixedEmbedProvider{vector: vec768(0)}}
	handler := NewRouter(&Handler{Retriever: retriever, DefaultTopK: totalChunks + 1})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/query", "application/json", strings.NewReader(`{"query":"anything"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got QueryResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	var mine []QueryResult
	for _, r := range got.Results {
		if r.Source == path {
			mine = append(mine, r)
		}
	}
	require.Len(t, mine, 2, "expected both of this test's own chunks back, filtered from whatever else may be in the shared test database")
	require.Equal(t, "exact match chunk", mine[0].Content)
	require.InDelta(t, 0, mine[0].Distance, 1e-6)
	require.Equal(t, "orthogonal chunk", mine[1].Content)
	require.Greater(t, mine[1].Distance, mine[0].Distance)
}
