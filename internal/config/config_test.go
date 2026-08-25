package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RejectsInvalidChunkParams(t *testing.T) {
	cases := []struct {
		name         string
		chunkSize    string
		chunkOverlap string
	}{
		{name: "overlap equal to size", chunkSize: "1000", chunkOverlap: "1000"},
		{name: "overlap greater than size", chunkSize: "500", chunkOverlap: "600"},
		{name: "zero size", chunkSize: "0", chunkOverlap: "0"},
		{name: "negative overlap", chunkSize: "1000", chunkOverlap: "-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CHUNK_SIZE", tc.chunkSize)
			t.Setenv("CHUNK_OVERLAP", tc.chunkOverlap)

			_, err := Load()
			require.Error(t, err, "CHUNK_SIZE=%s CHUNK_OVERLAP=%s should fail validation", tc.chunkSize, tc.chunkOverlap)
		})
	}
}

func TestLoad_AcceptsValidChunkParams(t *testing.T) {
	t.Setenv("CHUNK_SIZE", "800")
	t.Setenv("CHUNK_OVERLAP", "100")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 800, cfg.ChunkSize)
	assert.Equal(t, 100, cfg.ChunkOverlap)
}

func TestLoad_DefaultsWhenUnset(t *testing.T) {
	for _, key := range []string{
		"CHUNK_SIZE", "CHUNK_OVERLAP", "DATABASE_URL", "EMBEDDING_PROVIDER",
		"OLLAMA_URL", "OLLAMA_EMBED_MODEL", "SERVER_ADDR", "TOP_K",
	} {
		require.NoError(t, os.Unsetenv(key))
	}

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 1000, cfg.ChunkSize)
	assert.Equal(t, 200, cfg.ChunkOverlap)
	assert.Equal(t, "ollama", cfg.EmbeddingProvider)
	assert.Equal(t, ":8080", cfg.ServerAddr)
	assert.Equal(t, 5, cfg.TopK)
}
