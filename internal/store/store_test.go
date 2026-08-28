package store

import (
	"context"
	"os"
	"testing"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStore opens a Store against DATABASE_URL, skipping the test entirely
// if it's unset — so `go test ./...` works with no Postgres running, and
// only exercises this package when DATABASE_URL points at a real database
// (docker-compose locally, the service container in CI). The target
// database is assumed to already have migrations applied — `make test`
// runs `make migrate`'s equivalent against TEST_DATABASE_URL before `go
// test` starts, same precondition Open itself now documents.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping test that needs a real Postgres")
	}

	s, err := Open(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

// cleanupDocument deletes the document row (and, via ON DELETE CASCADE, its
// chunks) so repeated local runs against the same docker-compose database
// don't accumulate leftover rows from previous runs.
func cleanupDocument(t *testing.T, s *Store, path string) {
	t.Helper()
	t.Cleanup(func() {
		err := s.DeleteDocument(context.Background(), path)
		assert.NoError(t, err, "cleanup: delete document %q", path)
	})
}

// vec768 returns a 768-dim embedding with 1.0 at each given index and 0
// elsewhere — enough to construct vectors with known cosine distances
// (identical indices = distance 0, disjoint indices = distance 1, partial
// overlap = in between) without needing real embeddings.
func vec768(indices ...int) []float32 {
	v := make([]float32, 768)
	for _, i := range indices {
		v[i] = 1
	}
	return v
}

func TestUpsertDocumentAndReplaceChunks_RoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::round_trip"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	require.NoError(t, err)

	embedding := make([]float32, 768)
	embedding[0] = 0.5
	embedding[767] = -0.25

	err = s.ReplaceChunks(ctx, docID, []Chunk{
		{Index: 0, Content: "first chunk", Embedding: embedding},
	})
	require.NoError(t, err)

	var gotContent string
	var gotEmbedding pgvector.Vector
	row := s.pool.QueryRow(ctx,
		`SELECT content, embedding FROM chunks WHERE document_id = $1 AND chunk_index = 0`,
		docID)
	require.NoError(t, row.Scan(&gotContent, &gotEmbedding))
	assert.Equal(t, "first chunk", gotContent)

	gotSlice := gotEmbedding.Slice()
	require.Len(t, gotSlice, len(embedding))
	assert.Equal(t, float32(0.5), gotSlice[0])
	assert.Equal(t, float32(-0.25), gotSlice[767])
}

func TestReplaceChunks_ReplacesNotDuplicates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::replace_not_duplicate"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	require.NoError(t, err)

	emptyVec := make([]float32, 768)
	firstRun := []Chunk{
		{Index: 0, Content: "a", Embedding: emptyVec},
		{Index: 1, Content: "b", Embedding: emptyVec},
		{Index: 2, Content: "c", Embedding: emptyVec},
	}
	require.NoError(t, s.ReplaceChunks(ctx, docID, firstRun))

	secondRun := []Chunk{
		{Index: 0, Content: "x", Embedding: emptyVec},
	}
	require.NoError(t, s.ReplaceChunks(ctx, docID, secondRun))

	var count int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE document_id = $1`, docID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, len(secondRun), count, "chunk count after second ReplaceChunks should reflect a replace, not an append")
}

func TestSearchChunks_OrdersByDistanceAndLimitsToTopK(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::search_chunks"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	require.NoError(t, err)

	err = s.ReplaceChunks(ctx, docID, []Chunk{
		{Index: 0, Content: "orthogonal", Embedding: vec768(1)},
		{Index: 1, Content: "exact match", Embedding: vec768(0)},
		{Index: 2, Content: "partial match", Embedding: vec768(0, 1)},
	})
	require.NoError(t, err)

	results, err := s.SearchChunks(ctx, vec768(0), "irrelevant text", SearchVector, 2)
	require.NoError(t, err)
	require.Len(t, results, 2, "topK=2 should limit to 2 results even though 3 chunks exist for this document")

	assert.Equal(t, "exact match", results[0].Content)
	assert.Equal(t, path, results[0].Source)
	assert.Equal(t, 1, results[0].ChunkIndex)
	assert.InDelta(t, 0, results[0].Distance, 1e-6)

	assert.Equal(t, "partial match", results[1].Content)
	assert.Greater(t, results[1].Distance, results[0].Distance, "a partial match should be farther than an exact one")
}

func TestSearchChunks_KeywordAndAutoFindExactTerms(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::search_fts"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	require.NoError(t, err)

	// Every embedding is identical, so the vector side can't distinguish
	// these chunks — only full-text search can pick out the rare term.
	err = s.ReplaceChunks(ctx, docID, []Chunk{
		{Index: 0, Content: "the weather is mild today", Embedding: vec768(0)},
		{Index: 1, Content: "a lone quetzalcoatlus soared overhead", Embedding: vec768(0)},
		{Index: 2, Content: "nothing unusual happened at all", Embedding: vec768(0)},
	})
	require.NoError(t, err)

	t.Run("keyword returns exactly the matching chunk", func(t *testing.T) {
		results, err := s.SearchChunks(ctx, vec768(0), "quetzalcoatlus", SearchKeyword, 5)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, 1, results[0].ChunkIndex)
		assert.Positive(t, results[0].Score, "keyword hits carry a ts_rank score, not a distance")
	})

	t.Run("auto ranks the keyword match first even when vectors are tied", func(t *testing.T) {
		// topK spans all chunks so every one is in the vector sub-ranking
		// too; RRF still floats the keyword match to the top because it's
		// the only chunk scoring on both sides.
		results, err := s.SearchChunks(ctx, vec768(0), "quetzalcoatlus", SearchAuto, 3)
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, 1, results[0].ChunkIndex, "the exact-term chunk should rank first under RRF")
	})
}

func TestChunkContents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::chunk_contents"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "h")
	require.NoError(t, err)
	require.NoError(t, s.ReplaceChunks(ctx, docID, []Chunk{
		{Index: 0, Content: "chunk zero text", Embedding: vec768(0)},
		{Index: 1, Content: "chunk one text", Embedding: vec768(0)},
		{Index: 2, Content: "chunk two text", Embedding: vec768(0)},
	}))

	got, err := s.ChunkContents(ctx, path, []int{0, 2, 99})
	require.NoError(t, err)
	assert.Equal(t, map[int]string{0: "chunk zero text", 2: "chunk two text"}, got,
		"returns the requested chunks by index; a missing index is simply absent")

	empty, err := s.ChunkContents(ctx, path, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestIngestDirHash_RoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, err := s.pool.Exec(context.Background(), `DELETE FROM ingest_state WHERE id = 1`)
		assert.NoError(t, err)
	})

	require.NoError(t, s.SetIngestDirHash(ctx, "hash-1"))
	got, err := s.GetIngestDirHash(ctx)
	require.NoError(t, err)
	assert.Equal(t, "hash-1", got)

	require.NoError(t, s.SetIngestDirHash(ctx, "hash-2"))
	got, err = s.GetIngestDirHash(ctx)
	require.NoError(t, err)
	assert.Equal(t, "hash-2", got, "SetIngestDirHash should upsert the singleton row, not insert a second one")
}

func TestListDocuments(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const pathA = "store_test.go::list_docs_a"
	const pathB = "store_test.go::list_docs_b"
	cleanupDocument(t, s, pathA)
	cleanupDocument(t, s, pathB)

	emptyVec := make([]float32, 768)
	idA, err := s.UpsertDocument(ctx, pathA, "h")
	require.NoError(t, err)
	require.NoError(t, s.ReplaceChunks(ctx, idA, []Chunk{
		{Index: 0, Content: "a0", Embedding: emptyVec},
		{Index: 1, Content: "a1", Embedding: emptyVec},
	}))
	idB, err := s.UpsertDocument(ctx, pathB, "h")
	require.NoError(t, err)
	require.NoError(t, s.ReplaceChunks(ctx, idB, []Chunk{
		{Index: 0, Content: "b0", Embedding: emptyVec},
	}))

	docs, err := s.ListDocuments(ctx)
	require.NoError(t, err)

	got := map[string]int{}
	for _, d := range docs {
		got[d.Path] = d.Chunks
	}
	assert.Equal(t, 2, got[pathA])
	assert.Equal(t, 1, got[pathB])
}
