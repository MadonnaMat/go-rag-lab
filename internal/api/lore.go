package api

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/MadonnaMat/go-rag-lab/internal/chunk"
	"github.com/MadonnaMat/go-rag-lab/internal/lore"
)

// loreMarkdown renders GitHub-Flavored Markdown (tables, strikethrough,
// autolinks) — the lore docs are plain prose today, but GFM is the
// least-surprising default.
var loreMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// loreSanitizer strips anything the rendered markdown shouldn't carry into
// the page, while keeping the <mark class="cited"> spans this package adds
// for highlighted passages.
var loreSanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("mark")
	return p
}()

// Highlight sentinels wrap a cited passage in the markdown *source* before
// rendering, so goldmark treats them as ordinary text; they're swapped for
// real <mark> tags in the rendered HTML afterwards. Unicode private-use
// runes: they pass goldmark and the sanitizer through untouched (NUL would
// be rewritten to U+FFFD per CommonMark) and won't occur in a lore doc.
const (
	hlOpen  = ""
	hlClose = ""
)

// LoreResponse is the GET /lore/{name} response body.
type LoreResponse struct {
	HTML string `json:"html"`
}

// handleLore godoc
//
//	@Summary		Render an ingested document
//	@Description	Renders one LORE_DIR markdown file to sanitized HTML. Repeat ?chunks=<i> to wrap those chunks' text in <mark class="cited"> for the chat "sources" drawer. A plain idempotent read, like /query.
//	@Tags			chat
//	@Produce		json
//	@Param			name	path		string	true	"Document filename, e.g. 03-ulmarin-diet.md"
//	@Param			chunks	query		[]int	false	"Chunk indices to highlight (repeat the param)"
//	@Success		200		{object}	LoreResponse
//	@Failure		400		{object}	map[string]string
//	@Failure		404		{object}	map[string]string
//	@Router			/lore/{name} [get]
func (h *Handler) handleLore(w http.ResponseWriter, r *http.Request) {
	name, err := lore.SafeName(chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	md, err := os.ReadFile(filepath.Join(h.LoreDir, name)) //nolint:gosec // name is lore.SafeName-validated, inside LoreDir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "no such document "+strconv.Quote(name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Repeated query params (?chunks=3&chunks=4); a non-integer value is
	// dropped, so a malformed URL degrades to "render with no highlights".
	var chunkIdx []int
	for _, v := range r.URL.Query()["chunks"] {
		if n, convErr := strconv.Atoi(v); convErr == nil {
			chunkIdx = append(chunkIdx, n)
		}
	}

	html, err := renderLoreHTML(md, chunkIdx, h.ChunkSize, h.ChunkOverlap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, LoreResponse{HTML: html})
}

// renderLoreHTML renders markdown to sanitized HTML, wrapping the text of
// each requested chunk (by index, per chunk.Split with the given
// parameters) in <mark class="cited">. Out-of-range indices and chunks
// whose text can't be located in the source are skipped.
func renderLoreHTML(md []byte, chunkIdx []int, size, overlap int) (string, error) {
	marked, err := markSource(md, chunkIdx, size, overlap)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := loreMarkdown.Convert([]byte(marked), &buf); err != nil {
		return "", err
	}

	safe := loreSanitizer.SanitizeBytes(buf.Bytes())
	// The sentinels survive sanitising as bare text; swap them for real tags
	// now, past the policy.
	return strings.NewReplacer(
		hlOpen, `<mark class="cited">`,
		hlClose, `</mark>`,
	).Replace(string(safe)), nil
}

// markSource inserts the hlOpen/hlClose sentinels around each requested
// chunk's byte range in md, merging overlapping ranges so the sentinels
// stay balanced.
func markSource(md []byte, chunkIdx []int, size, overlap int) (string, error) {
	if len(chunkIdx) == 0 {
		return string(md), nil
	}

	chunks, err := chunk.Split(string(md), size, overlap)
	if err != nil {
		return "", err
	}

	text := string(md)
	type span struct{ start, end int }
	var spans []span
	for _, idx := range chunkIdx {
		if idx < 0 || idx >= len(chunks) {
			continue
		}
		if at := strings.Index(text, chunks[idx].Text); at >= 0 {
			spans = append(spans, span{at, at + len(chunks[idx].Text)})
		}
	}
	if len(spans) == 0 {
		return text, nil
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := []span{spans[0]}
	for _, s := range spans[1:] {
		last := &merged[len(merged)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}

	var b strings.Builder
	prev := 0
	for _, s := range merged {
		b.WriteString(text[prev:s.start])
		b.WriteString(hlOpen)
		b.WriteString(text[s.start:s.end])
		b.WriteString(hlClose)
		prev = s.end
	}
	b.WriteString(text[prev:])
	return b.String(), nil
}
