package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/chunk"
)

func TestRenderLoreHTML(t *testing.T) {
	const md = "# Title\n\nThe **first** paragraph mentions quetzalcoatlus here.\n\nA second paragraph is unrelated.\n"

	t.Run("renders markdown with no highlights", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), nil, 1000, 200)
		require.NoError(t, err)
		assert.Contains(t, html, "<h1>Title</h1>")
		assert.Contains(t, html, "<strong>first</strong>")
		assert.NotContains(t, html, "<mark")
	})

	t.Run("wraps a requested chunk's text in mark.cited", func(t *testing.T) {
		// Small chunks so a single chunk covers just part of the doc.
		chunks, err := chunk.Split(md, 40, 10)
		require.NoError(t, err)
		var withQ int
		for i, c := range chunks {
			if strings.Contains(c.Text, "quetzalcoatlus") {
				withQ = i
			}
		}

		html, err := renderLoreHTML([]byte(md), []int{withQ}, 40, 10)
		require.NoError(t, err)
		assert.Contains(t, html, `<mark class="cited">`)
		assert.Contains(t, html, "</mark>")
		assert.Contains(t, html, "quetzalcoatlus")
	})

	t.Run("ignores out-of-range indices", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), []int{99}, 1000, 200)
		require.NoError(t, err)
		assert.NotContains(t, html, "<mark")
	})

	t.Run("strips dangerous markup", func(t *testing.T) {
		html, err := renderLoreHTML([]byte("ok\n\n<script>alert(1)</script>\n"), nil, 1000, 200)
		require.NoError(t, err)
		assert.NotContains(t, html, "<script")
	})

	t.Run("merges overlapping highlight ranges into balanced marks", func(t *testing.T) {
		chunks, err := chunk.Split(md, 40, 20)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(chunks), 2)

		all := make([]int, len(chunks))
		for i := range chunks {
			all[i] = i
		}
		html, err := renderLoreHTML([]byte(md), all, 40, 20)
		require.NoError(t, err)
		assert.Equal(t, strings.Count(html, `<mark class="cited">`), strings.Count(html, "</mark>"),
			"every opening mark should have a matching close")
	})
}

func TestHandleLore(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "03-ulmarin-diet.md"),
		[]byte("# Diet\n\nThey eat glowfronds.\n"), 0o600))

	h := &Handler{LoreDir: dir, ChunkSize: 1000, ChunkOverlap: 200}
	router := NewRouter(h)

	do := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	t.Run("happy path returns rendered html", func(t *testing.T) {
		rec := do("/lore/03-ulmarin-diet.md")
		require.Equal(t, http.StatusOK, rec.Code)
		var resp LoreResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Contains(t, resp.HTML, "<h1>Diet</h1>")
	})

	t.Run("chunks param highlights", func(t *testing.T) {
		rec := do("/lore/03-ulmarin-diet.md?chunks=0")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "cited")
	})

	t.Run("malformed chunks value degrades to no highlight", func(t *testing.T) {
		rec := do("/lore/03-ulmarin-diet.md?chunks=abc")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "<mark")
	})

	t.Run("path traversal is rejected", func(t *testing.T) {
		rec := do("/lore/..%2f..%2fetc%2fpasswd")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("non-markdown name is rejected", func(t *testing.T) {
		rec := do("/lore/notes.txt")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing file is 404", func(t *testing.T) {
		rec := do("/lore/99-nonexistent.md")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}
