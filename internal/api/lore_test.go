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
		assert.NotContains(t, html, "cited")
	})

	t.Run("tags the block a requested chunk covers with class=cited", func(t *testing.T) {
		const spaced = "# Title\n\nThe first paragraph mentions quetzalcoatlus here and runs on for a while so its chunk stays within it.\n\nA clearly separate second paragraph, far away.\n"
		chunks, err := chunk.Split(spaced, 60, 5)
		require.NoError(t, err)
		var withQ int
		for i, c := range chunks {
			if strings.Contains(c.Text, "quetzalcoatlus") {
				withQ = i
			}
		}

		html, err := renderLoreHTML([]byte(spaced), []int{withQ}, 60, 5)
		require.NoError(t, err)
		assert.Regexp(t, `<p class="cited">[^<]*quetzalcoatlus`, html)
		assert.NotContains(t, html, `<p class="cited">A clearly separate`,
			"a block the chunk doesn't reach should not be tagged")
	})

	t.Run("ignores out-of-range indices", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), []int{99}, 1000, 200)
		require.NoError(t, err)
		assert.NotContains(t, html, "cited")
	})

	t.Run("strips dangerous markup", func(t *testing.T) {
		html, err := renderLoreHTML([]byte("ok\n\n<script>alert(1)</script>\n"), nil, 1000, 200)
		require.NoError(t, err)
		assert.NotContains(t, html, "<script")
	})

	t.Run("a chunk spanning several blocks tags each of them, HTML stays valid", func(t *testing.T) {
		// One big chunk covering the whole doc.
		html, err := renderLoreHTML([]byte(md), []int{0}, 1000, 200)
		require.NoError(t, err)
		assert.Contains(t, html, `<h1 class="cited">`)
		assert.Contains(t, html, `<p class="cited">`)
		// No stray unclosed inline highlight tags.
		assert.NotContains(t, html, "<mark")
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
