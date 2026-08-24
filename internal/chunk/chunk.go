// Package chunk splits document text into overlapping pieces small enough
// to embed and retrieve individually.
package chunk

import (
	"fmt"
	"strings"
)

type Chunk struct {
	Index int
	Text  string
}

// Split breaks text into chunks of at most size runes, each overlapping the
// previous chunk by overlap runes so context near a boundary isn't lost
// entirely to one side. Runes (not bytes) are counted so a multi-byte UTF-8
// character is never split down the middle.
func Split(text string, size, overlap int) ([]Chunk, error) {
	if size <= 0 {
		return nil, fmt.Errorf("chunk size must be positive, got %d", size)
	}
	if overlap < 0 {
		return nil, fmt.Errorf("chunk overlap must be non-negative, got %d", overlap)
	}
	if overlap >= size {
		return nil, fmt.Errorf("chunk overlap (%d) must be smaller than chunk size (%d)", overlap, size)
	}

	runes := []rune(text)
	step := size - overlap

	var chunks []Chunk
	for start := 0; start < len(runes); start += step {
		end := min(start+size, len(runes))
		if t := strings.TrimSpace(string(runes[start:end])); t != "" {
			chunks = append(chunks, Chunk{Index: len(chunks), Text: t})
		}
		if end == len(runes) {
			break
		}
	}
	return chunks, nil
}
