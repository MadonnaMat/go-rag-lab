package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// scriptedServer replies to successive /api/chat requests with the given
// canned NDJSON bodies, in order (looping the last one if called more
// times than scripted) — and records each request body it received.
type scriptedServer struct {
	*httptest.Server
	responses [][]chatStreamLine
	requests  []chatRequest
	call      int
}

func newScriptedServer(responses ...[]chatStreamLine) *scriptedServer {
	s := &scriptedServer{responses: responses}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.requests = append(s.requests, req)

		idx := s.call
		if idx >= len(s.responses) {
			idx = len(s.responses) - 1
		}
		s.call++
		for _, line := range s.responses[idx] {
			b, _ := json.Marshal(line)
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n"))
		}
	}))
	return s
}

func okLines(content string) []chatStreamLine {
	return []chatStreamLine{{Message: chatMessage{Role: "assistant", Content: content}, Done: true}}
}

func toolCallLines(name string, args map[string]any) []chatStreamLine {
	tc := toolCall{}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return []chatStreamLine{{Message: chatMessage{Role: "assistant", ToolCalls: []toolCall{tc}}, Done: true}}
}

type fakeRetriever struct {
	results []store.SearchResult
	err     error
	calls   []string
}

func (f *fakeRetriever) Query(ctx context.Context, q string, topK int) ([]store.SearchResult, error) {
	f.calls = append(f.calls, q)
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func collectEvents(t *testing.T, c *Chatter, history []Message) []Event {
	t.Helper()
	var events []Event
	err := c.Run(context.Background(), history, func(e Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		events = append(events, Event{Type: EventError, Err: err})
	}
	return events
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func TestRun_EmptyHistoryRejected(t *testing.T) {
	c := &Chatter{}
	err := c.Run(context.Background(), nil, func(Event) error { return nil })
	require.Error(t, err)
}

func TestRun_NoToolCall(t *testing.T) {
	srv := newScriptedServer(okLines("Hi there"), okLines("OK"))
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, DefaultTopK: 3}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hello"}})

	types := eventTypes(events)
	assert.Contains(t, types, EventToken)
	assert.Contains(t, types, EventVerifying)
	assert.Contains(t, types, EventContextUsage)
	assert.Equal(t, EventDone, types[len(types)-1])
	assert.NotContains(t, types, EventToolCall)
	assert.NotContains(t, types, EventRevised)
}

func TestRun_SingleToolCall(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines("retrieve_documents", map[string]any{"query": "ulmarin tech"}),
		okLines("The answer is X"),
		okLines("OK"),
	)
	defer srv.Close()

	retriever := &fakeRetriever{results: []store.SearchResult{{Source: "a.md", Content: "chunk", Distance: 0.1}}}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: retriever, DefaultTopK: 3}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "what is X?"}})

	types := eventTypes(events)
	require.Contains(t, types, EventToolCall)
	require.Contains(t, types, EventToolResult)
	require.Contains(t, types, EventToken)
	assert.Equal(t, EventDone, types[len(types)-1])

	require.Len(t, retriever.calls, 1)
	assert.Equal(t, "ulmarin tech", retriever.calls[0])

	// The second request sent to Ollama should carry the tool result back.
	require.Len(t, srv.requests, 3)
	var sawToolMessage bool
	for _, m := range srv.requests[1].Messages {
		if m.Role == "tool" {
			sawToolMessage = true
			assert.Contains(t, m.Content, "chunk")
		}
	}
	assert.True(t, sawToolMessage, "second request should include the tool result message")
}

func TestRun_RetrieverError(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines("retrieve_documents", map[string]any{"query": "x"}),
		okLines("fallback answer"),
		okLines("OK"),
	)
	defer srv.Close()

	retriever := &fakeRetriever{err: fmt.Errorf("db unavailable")}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: retriever, DefaultTopK: 3}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "what is X?"}})

	types := eventTypes(events)
	assert.Equal(t, EventDone, types[len(types)-1])

	require.Len(t, srv.requests, 3)
	var sawErrorToolMessage bool
	for _, m := range srv.requests[1].Messages {
		if m.Role == "tool" && m.Content != "" {
			sawErrorToolMessage = true
		}
	}
	assert.True(t, sawErrorToolMessage)
}

func TestRun_UnknownTool(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines("some_other_tool", map[string]any{}),
		okLines("fallback answer"),
		okLines("OK"),
	)
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, DefaultTopK: 3}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})
	assert.Equal(t, EventDone, eventTypes(events)[len(eventTypes(events))-1])
}

func TestRun_MaxIterationsExceeded(t *testing.T) {
	srv := newScriptedServer(toolCallLines("retrieve_documents", map[string]any{"query": "x"}))
	defer srv.Close()

	c := &Chatter{
		Client:            NewOllamaChat(srv.URL, "test-model"),
		Retriever:         &fakeRetriever{},
		DefaultTopK:       3,
		MaxToolIterations: 2,
	}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})
	types := eventTypes(events)
	assert.Equal(t, EventError, types[len(types)-1])
}

func TestRun_CompactCommandSkipsModel(t *testing.T) {
	srv := newScriptedServer(okLines("should not be called for the main turn"))
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, ContextTokens: 1000}
	history := make([]Message, 0, 10)
	for i := 0; i < 8; i++ {
		history = append(history, Message{Role: "user", Content: "some long message content to pad things out"})
	}
	history = append(history, Message{Role: "user", Content: "/compact"})

	events := collectEvents(t, c, history)
	types := eventTypes(events)
	require.Contains(t, types, EventCompacting)
	require.Contains(t, types, EventCompacted)
	require.Contains(t, types, EventContextUsage)
	assert.Equal(t, EventDone, types[len(types)-1])
	assert.NotContains(t, types, EventToken)
	assert.NotContains(t, types, EventToolCall)
}

func TestRun_VerifyRevisesAnswer(t *testing.T) {
	srv := newScriptedServer(okLines("a wrong draft"), okLines("a corrected answer"))
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, DefaultTopK: 3}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})

	var revised string
	for _, e := range events {
		if e.Type == EventRevised {
			revised = e.Revised
		}
	}
	assert.Equal(t, "a corrected answer", revised)
}

func TestRun_ContextUsageEmittedAfterNormalTurn(t *testing.T) {
	srv := newScriptedServer(okLines("hi"), okLines("OK"))
	defer srv.Close()

	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: &fakeRetriever{}, ContextTokens: 4096}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "hi"}})

	var found bool
	for _, e := range events {
		if e.Type == EventContextUsage {
			found = true
			assert.Equal(t, 4096, e.ContextTokens)
			assert.Greater(t, e.UsedTokens, 0)
		}
	}
	assert.True(t, found)
}
