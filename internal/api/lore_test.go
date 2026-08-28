package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLoreHTML(t *testing.T) {
	const md = "# Title\n\nThe **first** paragraph mentions quetzalcoatlus here.\n\nA second paragraph is unrelated.\n"

	t.Run("renders markdown with no highlights", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), nil)
		require.NoError(t, err)
		assert.Contains(t, html, "<h1>Title</h1>")
		assert.Contains(t, html, "<strong>first</strong>")
		assert.NotContains(t, html, "cited")
	})

	t.Run("tags the block a cited chunk covers with class=cited", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), []string{"The **first** paragraph mentions quetzalcoatlus here."})
		require.NoError(t, err)
		assert.Regexp(t, `<p class="cited">.*quetzalcoatlus`, html)
		assert.NotContains(t, html, `<p class="cited">A second paragraph`,
			"a block the chunk doesn't reach should not be tagged")
	})

	t.Run("ignores cited text not present in the source", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), []string{"text from a since-edited version of the file"})
		require.NoError(t, err)
		assert.NotContains(t, html, "cited")
	})

	t.Run("strips dangerous markup", func(t *testing.T) {
		html, err := renderLoreHTML([]byte("ok\n\n<script>alert(1)</script>\n"), nil)
		require.NoError(t, err)
		assert.NotContains(t, html, "<script")
	})

	t.Run("a chunk spanning several blocks tags each of them, HTML stays valid", func(t *testing.T) {
		html, err := renderLoreHTML([]byte(md), []string{md[:len(md)-1]})
		require.NoError(t, err)
		assert.Contains(t, html, `<h1 class="cited">`)
		assert.Contains(t, html, `<p class="cited">`)
		assert.NotContains(t, html, "<mark", "no stray inline highlight tags")
	})
}

// fakeChunkSource returns canned chunk text by (path, index).
type fakeChunkSource struct {
	byPath map[string]map[int]string
}

func (f *fakeChunkSource) ChunkContents(_ context.Context, docPath string, indices []int) (map[int]string, error) {
	out := map[int]string{}
	for _, i := range indices {
		if t, ok := f.byPath[docPath][i]; ok {
			out[i] = t
		}
	}
	return out, nil
}

func TestHandleLore(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "03-ulmarin-diet.md"),
		[]byte("# Diet\n\nThey eat glowfronds.\n\nThey also drink nectar.\n"), 0o600))

	h := &Handler{
		LoreDir: dir,
		LoreChunks: &fakeChunkSource{byPath: map[string]map[int]string{
			"03-ulmarin-diet.md": {0: "They eat glowfronds."},
		}},
	}
	router := NewRouter(h)

	do := func(target string) (*httptest.ResponseRecorder, LoreResponse) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		var resp LoreResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec, resp
	}

	t.Run("happy path returns rendered html", func(t *testing.T) {
		rec, resp := do("/lore/03-ulmarin-diet.md")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, resp.HTML, "<h1>Diet</h1>")
	})

	t.Run("chunks param highlights only the cited block", func(t *testing.T) {
		rec, resp := do("/lore/03-ulmarin-diet.md?chunks=0")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, resp.HTML, `<p class="cited">They eat glowfronds.`)
		assert.NotContains(t, resp.HTML, `<p class="cited">They also drink nectar`)
	})

	t.Run("malformed chunks value degrades to no highlight", func(t *testing.T) {
		rec, resp := do("/lore/03-ulmarin-diet.md?chunks=abc")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, resp.HTML, "cited")
	})

	t.Run("path traversal is rejected", func(t *testing.T) {
		rec, _ := do("/lore/..%2f..%2fetc%2fpasswd")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("non-markdown name is rejected", func(t *testing.T) {
		rec, _ := do("/lore/notes.txt")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing file is 404", func(t *testing.T) {
		rec, _ := do("/lore/99-nonexistent.md")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}
