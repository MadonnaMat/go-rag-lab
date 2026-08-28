package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

type fakeRetriever struct {
	results  []store.SearchResult
	err      error
	lastQ    string
	lastMode store.SearchMode
	lastK    int
}

func (f *fakeRetriever) Query(_ context.Context, q string, mode store.SearchMode, topK int) ([]store.SearchResult, error) {
	f.lastQ, f.lastMode, f.lastK = q, mode, topK
	return f.results, f.err
}

type fakeDocLister struct {
	docs []store.DocumentInfo
	err  error
}

func (f *fakeDocLister) ListDocuments(context.Context) ([]store.DocumentInfo, error) {
	return f.docs, f.err
}

type fakeLoremaster struct {
	calls   []string
	content []byte
	chunks  int
}

func (f *fakeLoremaster) IngestFile(_ context.Context, identity string, content []byte) (int, error) {
	f.calls = append(f.calls, identity)
	f.content = content
	if f.chunks == 0 {
		return 1, nil
	}
	return f.chunks, nil
}

func run(t *testing.T, name string, args map[string]any, deps Deps) Result {
	t.Helper()
	tool, ok := Find(name)
	require.True(t, ok, "tool %q not registered", name)
	return tool.Run(context.Background(), Call{ID: "call_1", Name: name, Args: args}, deps)
}

func TestAvailable_GatedOnDeps(t *testing.T) {
	names := func(ts []Tool) []string {
		out := make([]string, len(ts))
		for i, tl := range ts {
			out[i] = tl.Name()
		}
		return out
	}

	assert.Equal(t, []string{"retrieve_documents"}, names(Available(Deps{Retriever: &fakeRetriever{}})))

	full := Deps{
		Retriever: &fakeRetriever{}, Docs: &fakeDocLister{},
		Loremaster: &fakeLoremaster{}, LoreDir: "lore_docs",
	}
	assert.ElementsMatch(t,
		[]string{"retrieve_documents", "list_resources", "get_resource", "lore_drop"},
		names(Available(full)))
}

func TestRetrieve(t *testing.T) {
	fr := &fakeRetriever{results: []store.SearchResult{{Source: "a.md", ChunkIndex: 4, Content: "chunk text", Distance: 0.1}}}
	res := run(t, "retrieve_documents", map[string]any{"query": "q", "top_k": float64(2), "mode": "keyword"}, Deps{Retriever: fr, DefaultTopK: 5})

	require.NoError(t, res.Err)
	assert.Equal(t, 2, fr.lastK)
	assert.Equal(t, store.SearchKeyword, fr.lastMode)
	assert.Len(t, res.Chunks, 1)
	assert.Contains(t, res.Content, "chunk text")
	assert.Contains(t, res.Content, `"chunk_index":4`)
	assert.Empty(t, res.Summary, "retrieve leaves Summary empty so the UI renders the chunk list")
}

func TestRetrieve_DefaultsToAutoMode(t *testing.T) {
	fr := &fakeRetriever{}
	res := run(t, "retrieve_documents", map[string]any{"query": "q"}, Deps{Retriever: fr, DefaultTopK: 5})
	require.NoError(t, res.Err)
	assert.Equal(t, store.SearchAuto, fr.lastMode)
}

func TestRetrieve_RejectsBadMode(t *testing.T) {
	res := run(t, "retrieve_documents", map[string]any{"query": "q", "mode": "fuzzy"}, Deps{Retriever: &fakeRetriever{}, DefaultTopK: 5})
	require.Error(t, res.Err)
}

func TestRetrieve_MissingQuery(t *testing.T) {
	res := run(t, "retrieve_documents", map[string]any{}, Deps{Retriever: &fakeRetriever{}, DefaultTopK: 5})
	require.Error(t, res.Err)
}

func TestListResources(t *testing.T) {
	deps := Deps{Docs: &fakeDocLister{docs: []store.DocumentInfo{{Path: "a.md", Chunks: 3}, {Path: "b.md", Chunks: 1}}}}
	res := run(t, "list_resources", map[string]any{}, deps)

	require.NoError(t, res.Err)
	assert.Equal(t, "2 document(s) in the corpus", res.Summary)
	assert.Contains(t, res.Content, `"name":"a.md"`)
	assert.Contains(t, res.Content, `"chunks":3`)
	assert.JSONEq(t, string(res.Payload), res.Content)
}

func TestGetResource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte("# Title\nbody text"), 0o644))

	res := run(t, "get_resource", map[string]any{"name": "test-fixture-doc.md"}, Deps{LoreDir: dir})
	require.NoError(t, res.Err)
	assert.Contains(t, res.Content, "body text")

	var payload struct{ Name, Content string }
	require.NoError(t, json.Unmarshal(res.Payload, &payload))
	assert.Equal(t, "test-fixture-doc.md", payload.Name)
}

func TestGetResource_RejectsTraversalAndMissing(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../secret.md", "nested/x.md", "notmarkdown.txt"} {
		res := run(t, "get_resource", map[string]any{"name": name}, Deps{LoreDir: dir})
		require.Error(t, res.Err, name)
	}
	res := run(t, "get_resource", map[string]any{"name": "missing.md"}, Deps{LoreDir: dir})
	require.ErrorContains(t, res.Err, "no such resource")
}

func TestLoreDrop_Create(t *testing.T) {
	dir := t.TempDir()
	lm := &fakeLoremaster{chunks: 2}
	res := run(t, "lore_drop", map[string]any{
		"filename": "test-fixture-doc.md",
		"content":  "# New\nbody",
	}, Deps{LoreDir: dir, Loremaster: lm})

	require.NoError(t, res.Err)
	assert.Contains(t, res.Content, `"action":"created"`)
	assert.Equal(t, []string{"test-fixture-doc.md"}, lm.calls)

	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "# New\nbody", string(got))
}

func TestLoreDrop_AppendKeepsExistingAndDropsDuplicates(t *testing.T) {
	dir := t.TempDir()
	original := "# Doc\n\nOriginal paragraph.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte(original), 0o644))

	lm := &fakeLoremaster{}
	// Model re-sends the whole doc plus one new paragraph.
	res := run(t, "lore_drop", map[string]any{
		"filename": "test-fixture-doc.md",
		"content":  "# Doc\n\nOriginal paragraph.\n\nBrand new paragraph.",
		"mode":     "append",
	}, Deps{LoreDir: dir, Loremaster: lm})

	require.NoError(t, res.Err)
	assert.Contains(t, res.Content, `"action":"appended"`)

	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Doc\n\nOriginal paragraph.\n\nBrand new paragraph.", string(got))
	assert.Equal(t, 1, strings.Count(string(got), "Original paragraph."))
	assert.Contains(t, string(lm.content), "Brand new paragraph.")
}

func TestLoreDrop_AppendKeepsInteriorParagraphThatCoincides(t *testing.T) {
	dir := t.TempDir()
	// The doc already contains a "## Notes" heading somewhere.
	original := "# Doc\n\n## Notes\n\nExisting note.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte(original), 0o644))

	lm := &fakeLoremaster{}
	// New section reuses the "## Notes" heading as an interior paragraph —
	// it must not be dropped just because it appears earlier in the file.
	res := run(t, "lore_drop", map[string]any{
		"filename": "test-fixture-doc.md",
		"content":  "## History\n\nSome history.\n\n## Notes\n\nA second, different note.",
		"mode":     "append",
	}, Deps{LoreDir: dir, Loremaster: lm})

	require.NoError(t, res.Err)
	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "## History")
	assert.Contains(t, string(got), "A second, different note.")
	assert.Equal(t, 2, strings.Count(string(got), "## Notes"), "the reused heading is kept, not filtered")
}

func TestLoreDrop_AppendNothingNew(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte("# Doc\n\nAll of it.\n"), 0o644))

	lm := &fakeLoremaster{}
	res := run(t, "lore_drop", map[string]any{
		"filename": "test-fixture-doc.md",
		"content":  "# Doc\n\nAll of it.",
		"mode":     "append",
	}, Deps{LoreDir: dir, Loremaster: lm})

	require.ErrorContains(t, res.Err, "nothing new")
	assert.Empty(t, lm.calls, "no re-ingest when nothing changed")
}

func TestLoreDrop_ReplaceOverwrites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte("old body"), 0o644))

	res := run(t, "lore_drop", map[string]any{
		"filename": "test-fixture-doc.md",
		"content":  "completely new body",
		"mode":     "replace",
	}, Deps{LoreDir: dir, Loremaster: &fakeLoremaster{}})

	require.NoError(t, res.Err)
	assert.Contains(t, res.Content, `"action":"replaced"`)
	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "completely new body", string(got))
}

func TestLoreDrop_RejectsBadFilename(t *testing.T) {
	res := run(t, "lore_drop", map[string]any{"filename": "../evil.md", "content": "x"}, Deps{LoreDir: t.TempDir(), Loremaster: &fakeLoremaster{}})
	require.Error(t, res.Err)
}
