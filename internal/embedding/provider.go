// Package embedding turns text into vector embeddings via a pluggable
// Provider, so the rest of the app never talks to a specific embedding
// backend directly.
package embedding

import (
	"context"
	"fmt"
)

// Provider embeds a batch of texts, returning one vector per input text in
// the same order.
type Provider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// New selects a Provider implementation by name. Adding a second backend
// (e.g. OpenAI) later means adding one new file plus one new case here —
// callers of New never change.
func New(name, ollamaURL, ollamaModel string) (Provider, error) {
	switch name {
	case "ollama", "":
		return NewOllama(ollamaURL, ollamaModel), nil
	default:
		return nil, fmt.Errorf("unknown embedding provider %q", name)
	}
}
