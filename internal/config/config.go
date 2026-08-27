// Package config reads process configuration from environment variables,
// applying defaults so the rest of the app never touches os.Getenv
// directly.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/MadonnaMat/go-rag-lab/internal/chunk"
)

type Config struct {
	DatabaseURL       string
	EmbeddingProvider string
	OllamaURL         string
	OllamaEmbedModel  string
	OllamaChatModel   string
	ChunkSize         int
	ChunkOverlap      int
	ServerAddr        string
	TopK              int
	// LoreDir is the document directory cmd/ingest reads and the chat
	// get_resource / lore_drop tools read and write. Same default as
	// cmd/ingest's -dir flag.
	LoreDir string
}

// Load reads Config from the environment, applying defaults for anything
// unset. It returns an error only if a numeric variable is set but not a
// valid integer.
func Load() (Config, error) {
	chunkSize, err := getIntEnv("CHUNK_SIZE", 1000)
	if err != nil {
		return Config{}, err
	}

	chunkOverlap, err := getIntEnv("CHUNK_OVERLAP", 200)
	if err != nil {
		return Config{}, err
	}

	if err := chunk.ValidateParams(chunkSize, chunkOverlap); err != nil {
		return Config{}, fmt.Errorf("invalid chunk config: %w", err)
	}

	topK, err := getIntEnv("TOP_K", 5)
	if err != nil {
		return Config{}, err
	}
	// Same reasoning as chunk.ValidateParams above: reject a bad TOP_K up
	// front, at config-load time, rather than letting it surface later as
	// a confusing 500 from retrieve.Retriever.Query's own "topK must be
	// positive" check on every default (top_k-omitted) request.
	if topK <= 0 {
		return Config{}, fmt.Errorf("TOP_K: must be positive, got %d", topK)
	}

	return Config{
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://rag:rag@localhost:5432/rag?sslmode=disable"),
		EmbeddingProvider: getEnv("EMBEDDING_PROVIDER", "ollama"),
		OllamaURL:         getEnv("OLLAMA_URL", "http://localhost:11434"),
		// Default must stay in sync with internal/store/migrations'
		// vector(768) column width and docker/ollama-ci/Dockerfile's
		// pre-baked model — see migrations/000001_init.up.sql's comment for
		// why.
		OllamaEmbedModel: getEnv("OLLAMA_EMBED_MODEL", "nomic-embed-text"),
		// Sized to stay under 8GB VRAM alongside the embedding model — see
		// scripts/ollama-dev's chat-model pull step.
		OllamaChatModel: getEnv("OLLAMA_CHAT_MODEL", "qwen3:8b"),
		ChunkSize:       chunkSize,
		ChunkOverlap:    chunkOverlap,
		ServerAddr:      getEnv("SERVER_ADDR", ":8080"),
		TopK:            topK,
		LoreDir:         getEnv("LORE_DIR", "lore_docs"),
	}, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getIntEnv(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", key, v, err)
	}
	return n, nil
}
