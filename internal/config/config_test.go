package config

import (
	"os"
	"testing"
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

			if _, err := Load(); err == nil {
				t.Fatalf("Load() with CHUNK_SIZE=%s CHUNK_OVERLAP=%s returned nil error, want a validation error", tc.chunkSize, tc.chunkOverlap)
			}
		})
	}
}

func TestLoad_AcceptsValidChunkParams(t *testing.T) {
	t.Setenv("CHUNK_SIZE", "800")
	t.Setenv("CHUNK_OVERLAP", "100")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.ChunkSize != 800 || cfg.ChunkOverlap != 100 {
		t.Errorf("ChunkSize/ChunkOverlap = %d/%d, want 800/100", cfg.ChunkSize, cfg.ChunkOverlap)
	}
}

func TestLoad_DefaultsWhenUnset(t *testing.T) {
	for _, key := range []string{"CHUNK_SIZE", "CHUNK_OVERLAP", "DATABASE_URL", "EMBEDDING_PROVIDER", "OLLAMA_URL", "OLLAMA_EMBED_MODEL"} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.ChunkSize != 1000 || cfg.ChunkOverlap != 200 {
		t.Errorf("default ChunkSize/ChunkOverlap = %d/%d, want 1000/200", cfg.ChunkSize, cfg.ChunkOverlap)
	}
	if cfg.EmbeddingProvider != "ollama" {
		t.Errorf("default EmbeddingProvider = %q, want %q", cfg.EmbeddingProvider, "ollama")
	}
}
