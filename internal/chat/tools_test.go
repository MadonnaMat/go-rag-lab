package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

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
	err     error
}

func (f *fakeLoremaster) IngestFile(_ context.Context, identity string, content []byte) (int, error) {
	f.calls = append(f.calls, identity)
	f.content = content
	if f.err != nil {
		return 0, f.err
	}
	if f.chunks == 0 {
		return 1, nil
	}
	return f.chunks, nil
}

// toolMessages returns the role:"tool" message contents from the request
// the scripted server received at index i.
func toolMessages(req chatRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestRun_ListResourcesTool(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines(listResourcesToolName, map[string]any{}),
		okLines("there are two docs"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{
		Client:    NewOllamaChat(srv.URL, "test-model"),
		Retriever: &fakeRetriever{},
		Docs:      &fakeDocLister{docs: []store.DocumentInfo{{Path: "a.md", Chunks: 3}, {Path: "b.md", Chunks: 1}}},
	}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "what docs do you have?"}})

	types := eventTypes(events)
	require.Contains(t, types, EventToolCall)
	require.Contains(t, types, EventToolResult)
	assert.Equal(t, EventDone, types[len(types)-1])

	require.GreaterOrEqual(t, len(srv.requests), 2)
	msgs := toolMessages(srv.requests[1])
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `"name":"a.md"`)
	assert.Contains(t, msgs[0], `"chunks":3`)

	for _, e := range events {
		if e.Type == EventToolResult {
			assert.Equal(t, "2 document(s) in the corpus", e.ToolSummary)
		}
	}
}

func TestRun_ListResourcesToolNotWired(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines(listResourcesToolName, map[string]any{}),
		okLines("no listing"),
		okLines("OK"),
	)
	defer srv.Close()

	// No Docs set — tool is not advertised, and a call still gets a
	// structured error rather than crashing.
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})
	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])

	msgs := toolMessages(srv.requests[1])
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `"error"`)
}

func TestRun_GetResourceTool(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte("# Language\nfull text here"), 0o644))

	srv := newScriptedServer(
		toolCallLines(getResourceToolName, map[string]any{"name": "test-fixture-doc.md"}),
		okLines("here is the doc"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "show me the language doc"}})

	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])
	msgs := toolMessages(srv.requests[1])
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "full text here")
}

func TestRun_GetResourceToolRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	srv := newScriptedServer(
		toolCallLines(getResourceToolName, map[string]any{"name": "../secret.md"}),
		okLines("nope"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "read ../secret.md"}})

	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])
	msgs := toolMessages(srv.requests[1])
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `"error"`)
}

func TestRun_GetResourceToolNotFound(t *testing.T) {
	dir := t.TempDir()
	srv := newScriptedServer(
		toolCallLines(getResourceToolName, map[string]any{"name": "missing.md"}),
		okLines("nope"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "read missing.md"}})

	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])
	msgs := toolMessages(srv.requests[1])
	require.Contains(t, msgs[0], "no such resource")
}

func TestRun_LoreDropTool(t *testing.T) {
	dir := t.TempDir()
	srv := newScriptedServer(
		toolCallLines(loreDropToolName, map[string]any{
			"filename": "test-fixture-doc.md",
			"content":  "# Cuisine\nThe ulmarin eat moss.",
			"reason":   "corpus had nothing on food",
		}),
		okLines("added it"),
		okLines("OK"),
	)
	defer srv.Close()

	lm := &fakeLoremaster{chunks: 2}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir, Loremaster: lm}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "what do ulmarin eat?"}})

	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])

	// File landed on disk.
	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Cuisine\nThe ulmarin eat moss.", string(got))

	// Re-ingest was invoked with the bare filename.
	require.Equal(t, []string{"test-fixture-doc.md"}, lm.calls)

	msgs := toolMessages(srv.requests[1])
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `"action":"created"`)
	assert.Contains(t, msgs[0], `"chunks":2`)
}

func TestRun_LoreDropToolAppends(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte("# Ulmarin Cuisine\n\nThe original section.\n"), 0o644))

	srv := newScriptedServer(
		toolCallLines(loreDropToolName, map[string]any{
			"filename": "test-fixture-doc.md",
			"content":  "## New Section\n\nMore about their food.",
			"mode":     "append",
		}),
		okLines("appended it"),
		okLines("OK"),
	)
	defer srv.Close()

	lm := &fakeLoremaster{chunks: 3}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir, Loremaster: lm}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "tell me more about ulmarin food"}})
	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])

	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Ulmarin Cuisine\n\nThe original section.\n\n## New Section\n\nMore about their food.", string(got))

	// The tool re-ingested the full combined body, not just the fragment.
	assert.Contains(t, string(lm.content), "The original section.")
	assert.Contains(t, string(lm.content), "New Section")

	msgs := toolMessages(srv.requests[1])
	assert.Contains(t, msgs[0], `"action":"appended"`)
}

func TestRun_LoreDropToolAppendDropsDuplicateParagraphs(t *testing.T) {
	dir := t.TempDir()
	original := "# Ulmarin Cuisine\n\nThe original section.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte(original), 0o644))

	srv := newScriptedServer(
		// Model re-sends the whole doc plus one new paragraph (the observed
		// weak-model behaviour).
		toolCallLines(loreDropToolName, map[string]any{
			"filename": "test-fixture-doc.md",
			"content":  "# Ulmarin Cuisine\n\nThe original section.\n\nA genuinely new paragraph.",
			"mode":     "append",
		}),
		okLines("done"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir, Loremaster: &fakeLoremaster{}}
	collectEvents(t, c, []Message{{Role: "user", Content: "add lore"}})

	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Ulmarin Cuisine\n\nThe original section.\n\nA genuinely new paragraph.", string(got))
	assert.Equal(t, 1, strings.Count(string(got), "The original section."))
}

func TestRun_LoreDropToolAppendNothingNew(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-fixture-doc.md"), []byte("# Ulmarin Cuisine\n\nAll of it.\n"), 0o644))

	srv := newScriptedServer(
		toolCallLines(loreDropToolName, map[string]any{
			"filename": "test-fixture-doc.md",
			"content":  "# Ulmarin Cuisine\n\nAll of it.",
			"mode":     "append",
		}),
		okLines("hmm"),
		okLines("OK"),
	)
	defer srv.Close()

	lm := &fakeLoremaster{}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir, Loremaster: lm}
	collectEvents(t, c, []Message{{Role: "user", Content: "add lore"}})

	assert.Empty(t, lm.calls, "no re-ingest when there's nothing new")
	msgs := toolMessages(srv.requests[1])
	assert.Contains(t, msgs[0], `"error"`)
	assert.Contains(t, msgs[0], "nothing new")
}

func TestRun_LoreDropToolRejectsSecondCallSameTurn(t *testing.T) {
	dir := t.TempDir()
	srv := newScriptedServer(
		toolCallLines(loreDropToolName, map[string]any{"filename": "test-fixture-doc.md", "content": "first body"}),
		toolCallLines(loreDropToolName, map[string]any{"filename": "test-fixture-doc.md", "content": "reworded body", "mode": "append"}),
		okLines("here is your lore"),
		okLines("OK"),
	)
	defer srv.Close()

	lm := &fakeLoremaster{chunks: 1}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: dir, Loremaster: lm}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "add lore"}})
	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])

	// Only the first call actually wrote / re-ingested.
	require.Len(t, lm.calls, 1)
	got, err := os.ReadFile(filepath.Join(dir, "test-fixture-doc.md"))
	require.NoError(t, err)
	assert.Equal(t, "first body", string(got))

	// The second call came back as a structured error telling the model to stop.
	require.Len(t, srv.requests, 4)
	var secondResult string
	for _, m := range srv.requests[2].Messages {
		if m.Role == "tool" {
			secondResult = m.Content
		}
	}
	assert.Contains(t, secondResult, "already saved")
}

func TestRun_LoreDropToolNotWired(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines(loreDropToolName, map[string]any{"filename": "x.md", "content": "y"}),
		okLines("fallback"),
		okLines("OK"),
	)
	defer srv.Close()

	// LoreDir set but no Loremaster.
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, LoreDir: t.TempDir()}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})

	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])
	msgs := toolMessages(srv.requests[1])
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `"error"`)
}

func TestAvailableTools_GatedOnDeps(t *testing.T) {
	base := &Chatter{Client: NewOllamaChat("http://x", "m"), Retriever: &fakeRetriever{}}
	names := func(c *Chatter) []string {
		var out []string
		for _, tl := range c.availableTools() {
			out = append(out, tl.Function.Name)
		}
		return out
	}

	assert.Equal(t, []string{retrieveToolName}, names(base))

	full := &Chatter{
		Client: base.Client, Retriever: base.Retriever,
		Docs: &fakeDocLister{}, Loremaster: &fakeLoremaster{}, LoreDir: "lore_docs",
	}
	assert.ElementsMatch(t,
		[]string{retrieveToolName, listResourcesToolName, getResourceToolName, loreDropToolName},
		names(full))

	// verifyTools drops lore_drop.
	assert.NotContains(t, func() []string {
		var out []string
		for _, tl := range full.verifyTools() {
			out = append(out, tl.Function.Name)
		}
		return out
	}(), loreDropToolName)
}
