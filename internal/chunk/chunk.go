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

// ValidateParams checks that size and overlap are usable chunking
// parameters without needing any text to chunk yet, so a caller (e.g.
// config loading) can reject a bad combination up front instead of only
// discovering it when Split is first called on real content.
func ValidateParams(size, overlap int) error {
	if size <= 0 {
		return fmt.Errorf("chunk size must be positive, got %d", size)
	}
	if overlap < 0 {
		return fmt.Errorf("chunk overlap must be non-negative, got %d", overlap)
	}
	if overlap >= size {
		return fmt.Errorf("chunk overlap (%d) must be smaller than chunk size (%d)", overlap, size)
	}
	return nil
}

// Split breaks text into chunks of at most size runes, each overlapping the
// previous chunk by overlap runes so context near a boundary isn't lost
// entirely to one side. Runes (not bytes) are counted so a multi-byte UTF-8
// character is never split down the middle.
func Split(text string, size, overlap int) ([]Chunk, error) {
	if err := ValidateParams(size, overlap); err != nil {
		return nil, err
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
