// Package config reads process configuration from environment variables,
// applying defaults so the rest of the app never touches os.Getenv
// directly.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL       string
	EmbeddingProvider string
	OllamaURL         string
	OllamaEmbedModel  string
	ChunkSize         int
	ChunkOverlap      int
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

	return Config{
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://rag:rag@localhost:5432/rag?sslmode=disable"),
		EmbeddingProvider: getEnv("EMBEDDING_PROVIDER", "ollama"),
		OllamaURL:         getEnv("OLLAMA_URL", "http://localhost:11434"),
		OllamaEmbedModel:  getEnv("OLLAMA_EMBED_MODEL", "nomic-embed-text"),
		ChunkSize:         chunkSize,
		ChunkOverlap:      chunkOverlap,
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
