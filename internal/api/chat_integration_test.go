package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/chat"
	"github.com/MadonnaMat/go-rag-lab/internal/retrieve"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// scriptedOllamaChatServer fakes Ollama's /api/chat: the first call
// returns a tool-call turn naming retrieve_documents, every later call
// returns a plain-text final answer — enough to drive one real
// tool-calling round trip without a live Ollama server.
func scriptedOllamaChatServer(t *testing.T, finalAnswer string) *httptest.Server {
	t.Helper()
	var call int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","function":{"name":"retrieve_documents","arguments":{"query":"ulmarin technology","top_k":5}}}]},"done":false}`)
			_, _ = fmt.Fprintln(w, `{"message":{"role":"assistant","content":""},"done":true}`)
			return
		}
		line := map[string]any{
			"message": map[string]any{"role": "assistant", "content": finalAnswer},
			"done":    true,
		}
		b, _ := json.Marshal(line)
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n"))
	}))
}

// TestChatEndToEnd wires a real Store, real Retriever, and a real
// chat.Chatter (talking to a scripted fake Ollama, not a live one) behind
// a real HTTP listener, proving the retrieval tool actually round-trips
// through Postgres data and back out as SSE events. Needs DATABASE_URL
// but no live Ollama.
func TestChatEndToEnd(t *testing.T) {
	dbURL := integrationDatabaseURL(t)
	ctx := context.Background()

	s, err := store.Open(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	const path = "api_integration_test.go::chat_e2e"
	t.Cleanup(func() {
		require.NoError(t, s.DeleteDocument(context.Background(), path))
	})

	docID, err := s.UpsertDocument(ctx, path, "hash-1")
	require.NoError(t, err)
	require.NoError(t, s.ReplaceChunks(ctx, docID, []store.Chunk{
		{Index: 0, Content: "the ulmarin grow vent-forges to shape minerals", Embedding: vec768(0)},
	}))

	ollama := scriptedOllamaChatServer(t, "The Ulmarin use cultivated biology.")
	defer ollama.Close()

	retriever := &retrieve.Retriever{Store: s, Provider: fixedEmbedProvider{vector: vec768(0)}}
	chatter := &chat.Chatter{
		Client:      chat.NewOllamaChat(ollama.URL, "test-model"),
		Retriever:   retriever,
		DefaultTopK: 5,
	}

	handler := NewRouter(&Handler{Retriever: retriever, Chatter: chatter, DefaultTopK: 5})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"messages":[{"role":"user","content":"how did the ulmarin build things?"}]}`
	resp, err := http.Post(srv.URL+"/chat", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		out.WriteString(scanner.Text())
		out.WriteString("\n")
	}
	got := out.String()

	assert.Contains(t, got, "event: tool_call")
	assert.Contains(t, got, "retrieve_documents")
	assert.Contains(t, got, "event: tool_result")
	assert.Contains(t, got, "the ulmarin grow vent-forges to shape minerals", "tool_result should carry the seeded chunk's real content back through Postgres")
	assert.Contains(t, got, "event: token")
	assert.Contains(t, got, "The Ulmarin use cultivated biology.")
	assert.Contains(t, got, "event: done")
}
