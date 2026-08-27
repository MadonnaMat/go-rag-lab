package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// dirHash returns a stable hash over a directory's regular-file contents,
// keyed by filename, plus the chunking parameters that would affect how
// those files get split — used to detect an unchanged source directory
// (and unchanged chunking config) between ingestion runs so re-running
// can skip re-chunking, re-embedding, and re-summarizing entirely. Adding,
// removing, renaming, or modifying any file, or changing chunkSize/
// chunkOverlap, changes the result. Without folding chunkSize/chunkOverlap
// in, editing chunk config and re-running against an unchanged directory
// would silently skip re-ingestion and leave stale chunks in the store.
func dirHash(files map[string][]byte, chunkSize, chunkOverlap int) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	h.Write([]byte(strconv.Itoa(chunkSize)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(chunkOverlap)))
	h.Write([]byte{0})
	for _, name := range names {
		fileSum := sha256.Sum256(files[name])
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(fileSum[:])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
