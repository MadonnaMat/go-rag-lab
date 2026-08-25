package store

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// migrationsDatabaseURL returns DATABASE_URL, skipping the test entirely if
// it's unset — same reasoning as testStore in store_test.go.
func migrationsDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping test that needs a real Postgres")
	}
	return url
}

// TestMigrateUp_Idempotent exercises the property MigrateUp relies on:
// running it again against an already-migrated database is a no-op, not an
// error — MigrateUp swallows migrate.ErrNoChange for exactly this case.
// make test / make migrate already applied migrations once before this
// test ran, so this call is that "run it again" case.
func TestMigrateUp_Idempotent(t *testing.T) {
	url := migrationsDatabaseURL(t)
	require.NoError(t, MigrateUp(url), "MigrateUp (second application)")
}

// TestMigrateUp_CreatesEmbeddingIndex confirms migration 000002 actually
// landed: the HNSW index SearchChunks' query plan depends on.
func TestMigrateUp_CreatesEmbeddingIndex(t *testing.T) {
	url := migrationsDatabaseURL(t)
	s, err := Open(context.Background(), url)
	require.NoError(t, err)
	defer s.Close()

	var exists bool
	err = s.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'chunks_embedding_hnsw_idx')`,
	).Scan(&exists)
	require.NoError(t, err)
	require.True(t, exists, "chunks_embedding_hnsw_idx does not exist; want migration 000002 to have created it")
}
