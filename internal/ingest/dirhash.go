package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// dirHash returns a stable hash over a directory's regular-file contents,
// keyed by filename — used to detect an unchanged source directory
// between ingestion runs so re-running can skip re-chunking,
// re-embedding, and re-summarizing entirely. Adding, removing, renaming,
// or modifying any file changes the result.
func dirHash(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		fileSum := sha256.Sum256(files[name])
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(fileSum[:])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
