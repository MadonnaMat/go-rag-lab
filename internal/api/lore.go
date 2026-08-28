package api

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"github.com/MadonnaMat/go-rag-lab/internal/lore"
)

// LoreChunkSource returns the exact ingested text of specific chunks of a
// document, keyed by chunk index (see store.Store.ChunkContents). /lore
// highlights against that text rather than re-splitting the on-disk file,
// so an edit to the file or a chunk-config change between ingestion and
// serving can only cause a missed highlight, never a wrong one.
type LoreChunkSource interface {
	ChunkContents(ctx context.Context, docPath string, indices []int) (map[int]string, error)
}

// loreSanitizer strips anything the rendered markdown shouldn't carry into
// the page, while keeping the class="cited" the block highlighter adds.
var loreSanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).Globally()
	return p
}()

// loreMarkdown renders GitHub-Flavored Markdown (tables, strikethrough,
// autolinks) — the lore docs are plain prose today, but GFM is the
// least-surprising default.
var loreMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// LoreResponse is the GET /lore/{name} response body.
type LoreResponse struct {
	HTML string `json:"html"`
}

// handleLore godoc
//
//	@Summary		Render an ingested document
//	@Description	Renders one LORE_DIR markdown file to sanitized HTML. Repeat ?chunks=<i> to add class="cited" to the blocks those chunks cover, for the chat "sources" drawer. A plain idempotent read, like /query.
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

	var citedText []string
	if len(chunkIdx) > 0 && h.LoreChunks != nil {
		byIdx, ccErr := h.LoreChunks.ChunkContents(r.Context(), name, chunkIdx)
		if ccErr != nil {
			writeError(w, http.StatusInternalServerError, ccErr.Error())
			return
		}
		for _, idx := range chunkIdx {
			if t, ok := byIdx[idx]; ok {
				citedText = append(citedText, t)
			}
		}
	}

	html, err := renderLoreHTML(md, citedText)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, LoreResponse{HTML: html})
}

// renderLoreHTML renders markdown to sanitized HTML. Every top-level block
// (paragraph, heading, list, blockquote, code block) that overlaps the
// source span of one of citedText — the exact ingested text of a cited
// chunk — gets class="cited" so the frontend can shade it. Block-level
// rather than word-level keeps the HTML valid even when a chunk starts or
// ends mid-block. A chunk whose text isn't found in the source (the file
// changed since ingestion) is silently skipped.
func renderLoreHTML(md []byte, citedText []string) (string, error) {
	ranges := citedRanges(md, citedText)

	doc := loreMarkdown.Parser().Parse(text.NewReader(md))
	if len(ranges) > 0 {
		markCitedBlocks(doc, ranges)
	}

	var buf bytes.Buffer
	if err := loreMarkdown.Renderer().Render(&buf, md, doc); err != nil {
		return "", err
	}
	return string(loreSanitizer.SanitizeBytes(buf.Bytes())), nil
}

type byteRange struct{ start, end int }

// citedRanges locates each cited chunk's text as a substring of md. Chunk
// text is TrimSpace of a verbatim rune-window of the ingested source, so a
// direct substring match is reliable while the file is unchanged.
func citedRanges(md []byte, citedText []string) []byteRange {
	var out []byteRange
	for _, t := range citedText {
		if t == "" {
			continue
		}
		if at := bytes.Index(md, []byte(t)); at >= 0 {
			out = append(out, byteRange{at, at + len(t)})
		}
	}
	return out
}

// markCitedBlocks tags every top-level block whose source span overlaps a
// cited range with class="cited". Only the document's direct children are
// considered, so a cited list is shaded as one block rather than the list
// plus each item plus each paragraph.
func markCitedBlocks(doc ast.Node, ranges []byteRange) {
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		s, e, ok := blockSpan(n)
		if !ok {
			continue
		}
		for _, r := range ranges {
			if s < r.end && r.start < e {
				n.SetAttributeString("class", []byte("cited"))
				break
			}
		}
	}
}

// blockSpan returns the source byte span a block node covers, from its own
// text lines or, for container blocks (lists, blockquotes), the union of
// its descendants' lines. ok is false for a block with no source lines.
func blockSpan(n ast.Node) (start, end int, ok bool) {
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		if node.Type() != ast.TypeBlock {
			return // Lines() panics on inline nodes
		}
		if lines := node.Lines(); lines != nil && lines.Len() > 0 {
			if s := lines.At(0).Start; !ok || s < start {
				start = s
			}
			if e := lines.At(lines.Len() - 1).Stop; e > end {
				end = e
			}
			ok = true
		}
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(n)
	return start, end, ok
}
