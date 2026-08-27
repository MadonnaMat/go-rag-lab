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
}

func (f *fakeDocLister) ListDocuments(context.Context) ([]store.DocumentInfo, error) {
	return f.docs, nil
}

type fakeLoremaster struct{ calls []string }

func (f *fakeLoremaster) IngestFile(_ context.Context, identity string, _ []byte) (int, error) {
	f.calls = append(f.calls, identity)
	return 1, nil
}

// toolResults returns the role:"tool" message contents from request i.
func toolResults(req chatRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestDispatch_ListResourcesFlowsPayloadToEvent(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines("list_resources", map[string]any{}),
		okLines("two docs"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{
		Client:    NewOllamaChat(srv.URL, "test-model"),
		Retriever: &fakeRetriever{},
		Docs:      &fakeDocLister{docs: []store.DocumentInfo{{Path: "a.md", Chunks: 3}}},
	}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "what docs?"}})

	var result Event
	for _, e := range events {
		if e.Type == EventToolResult {
			result = e
		}
	}
	assert.Equal(t, "1 document(s) in the corpus", result.ToolSummary)
	assert.JSONEq(t, `[{"name":"a.md","chunks":3}]`, string(result.ToolPayload))
	assert.Contains(t, toolResults(srv.requests[1])[0], `"name":"a.md"`)
}

func TestDispatch_UnavailableToolReturnsError(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines("list_resources", map[string]any{}),
		okLines("fallback"),
		okLines("OK"),
	)
	defer srv.Close()

	// No Docs wired: list_resources is neither advertised nor dispatchable.
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}}
	collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})

	assert.Contains(t, toolResults(srv.requests[1])[0], `"error"`)
}

func TestVerify_RefusesToWriteToCorpus(t *testing.T) {
	dir := t.TempDir()
	srv := newScriptedServer(
		// main turn: a plain answer, no tools
		okLines("here is an answer"),
		// verify turn: model tries to lore_drop
		toolCallLines("lore_drop", map[string]any{"filename": "test-fixture-doc.md", "content": "sneaky write"}),
		// verify follow-up after the refusal
		okLines("OK"),
	)
	defer srv.Close()

	lm := &fakeLoremaster{}
	c := &Chatter{
		Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{},
		LoreDir: dir, Loremaster: lm,
	}
	collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})

	assert.Empty(t, lm.calls, "verify pass must never write to the corpus")
	_, statErr := os.Stat(filepath.Join(dir, "test-fixture-doc.md"))
	assert.True(t, os.IsNotExist(statErr), "no file should have been created during verify")

	// The refusal is fed back to the model.
	var sawRefusal bool
	for _, req := range srv.requests {
		for _, m := range req.Messages {
			if m.Role == "tool" && strings.Contains(m.Content, "read-only step") {
				sawRefusal = true
			}
		}
	}
	assert.True(t, sawRefusal)
}

func TestDispatch_LoreDropOncePerTurn(t *testing.T) {
	dir := t.TempDir()
	srv := newScriptedServer(
		toolCallLines("lore_drop", map[string]any{"filename": "test-fixture-doc.md", "content": "first body"}),
		toolCallLines("lore_drop", map[string]any{"filename": "test-fixture-doc.md", "content": "second body", "mode": "append"}),
		okLines("here it is"),
		okLines("OK"),
	)
	defer srv.Close()

	lm := &fakeLoremaster{}
	c := &Chatter{
		Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{},
		LoreDir: dir, Loremaster: lm,
	}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "add lore"}})
	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])

	require.Len(t, lm.calls, 1, "only the first lore_drop should actually run")
	results := toolResults(srv.requests[2])
	require.Len(t, results, 2)
	assert.Contains(t, results[0], `"action":"created"`)
	assert.Contains(t, results[1], "already used in this turn")
}
