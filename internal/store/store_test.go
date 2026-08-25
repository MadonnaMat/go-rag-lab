package store

import (
	"context"
	"os"
	"testing"

	"github.com/pgvector/pgvector-go"
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
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// cleanupDocument deletes the document row (and, via ON DELETE CASCADE, its
// chunks) so repeated local runs against the same docker-compose database
// don't accumulate leftover rows from previous runs.
func cleanupDocument(t *testing.T, s *Store, path string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM documents WHERE path = $1`, path); err != nil {
			t.Errorf("cleanup: delete document %q: %v", path, err)
		}
	})
}

func TestUpsertDocumentAndReplaceChunks_RoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::round_trip"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	if err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}

	embedding := make([]float32, 768)
	embedding[0] = 0.5
	embedding[767] = -0.25

	err = s.ReplaceChunks(ctx, docID, []Chunk{
		{Index: 0, Content: "first chunk", Embedding: embedding},
	})
	if err != nil {
		t.Fatalf("ReplaceChunks: %v", err)
	}

	var gotContent string
	var gotEmbedding pgvector.Vector
	row := s.pool.QueryRow(ctx,
		`SELECT content, embedding FROM chunks WHERE document_id = $1 AND chunk_index = 0`,
		docID)
	if err := row.Scan(&gotContent, &gotEmbedding); err != nil {
		t.Fatalf("query inserted chunk: %v", err)
	}
	if gotContent != "first chunk" {
		t.Errorf("content = %q, want %q", gotContent, "first chunk")
	}
	gotSlice := gotEmbedding.Slice()
	if len(gotSlice) != len(embedding) {
		t.Fatalf("embedding length = %d, want %d", len(gotSlice), len(embedding))
	}
	if gotSlice[0] != 0.5 {
		t.Errorf("embedding[0] = %v, want 0.5", gotSlice[0])
	}
	if gotSlice[767] != -0.25 {
		t.Errorf("embedding[767] = %v, want -0.25", gotSlice[767])
	}
}

func TestReplaceChunks_ReplacesNotDuplicates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const path = "store_test.go::replace_not_duplicate"
	cleanupDocument(t, s, path)

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	if err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}

	emptyVec := make([]float32, 768)
	firstRun := []Chunk{
		{Index: 0, Content: "a", Embedding: emptyVec},
		{Index: 1, Content: "b", Embedding: emptyVec},
		{Index: 2, Content: "c", Embedding: emptyVec},
	}
	if err := s.ReplaceChunks(ctx, docID, firstRun); err != nil {
		t.Fatalf("ReplaceChunks (first run): %v", err)
	}

	secondRun := []Chunk{
		{Index: 0, Content: "x", Embedding: emptyVec},
	}
	if err := s.ReplaceChunks(ctx, docID, secondRun); err != nil {
		t.Fatalf("ReplaceChunks (second run): %v", err)
	}

	var count int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE document_id = $1`, docID).Scan(&count)
	if err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if count != len(secondRun) {
		t.Errorf("chunk count after second ReplaceChunks = %d, want %d (replaced, not appended)", count, len(secondRun))
	}
}
