package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaChat_StreamsTokenDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.True(t, req.Stream)
		assert.True(t, req.Think)

		lines := []chatStreamLine{
			{Message: chatMessage{Role: "assistant", Content: "Hello"}},
			{Message: chatMessage{Role: "assistant", Content: ", world"}},
			{Message: chatMessage{Role: "assistant", Content: "!"}, Done: true},
		}
		for _, l := range lines {
			b, _ := json.Marshal(l)
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n"))
		}
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "test-model")
	var got []chatStreamLine
	err := c.Chat(context.Background(), []chatMessage{{Role: "user", Content: "hi"}}, nil, func(line chatStreamLine) error {
		got = append(got, line)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "Hello", got[0].Message.Content)
	assert.Equal(t, ", world", got[1].Message.Content)
	assert.Equal(t, "!", got[2].Message.Content)
	assert.True(t, got[2].Done)
}

func TestOllamaChat_ParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		line := chatStreamLine{
			Message: chatMessage{
				Role: "assistant",
				ToolCalls: []toolCall{{Function: struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}{Name: "retrieve_documents", Arguments: map[string]any{"query": "test"}}}},
			},
			Done: true,
		}
		b, _ := json.Marshal(line)
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n"))
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "test-model")
	var got chatStreamLine
	err := c.Chat(context.Background(), nil, []toolDef{retrieveToolDef()}, func(line chatStreamLine) error {
		got = line
		return nil
	})
	require.NoError(t, err)
	require.Len(t, got.Message.ToolCalls, 1)
	assert.Equal(t, "retrieve_documents", got.Message.ToolCalls[0].Function.Name)
	assert.Equal(t, "test", got.Message.ToolCalls[0].Function.Arguments["query"])
}

func TestOllamaChat_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "test-model")
	err := c.Chat(context.Background(), nil, nil, func(chatStreamLine) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestOllamaChat_MalformedLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json\n"))
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "test-model")
	err := c.Chat(context.Background(), nil, nil, func(chatStreamLine) error { return nil })
	require.Error(t, err)
}

func TestOllamaChat_CanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewOllamaChat(srv.URL, "test-model")
	err := c.Chat(ctx, nil, nil, func(chatStreamLine) error { return nil })
	require.Error(t, err)
}

func TestOllamaChat_ContextLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/show", r.URL.Path)
		_ = json.NewEncoder(w).Encode(ollamaShowResponse{
			ModelInfo: map[string]any{"qwen3.context_length": float64(32768)},
		})
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "qwen3:8b")
	n, err := c.ContextLength(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 32768, n)
}

func TestOllamaChat_ContextLength_MissingFieldFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaShowResponse{ModelInfo: map[string]any{}})
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "qwen3:8b")
	n, err := c.ContextLength(context.Background())
	require.Error(t, err)
	assert.Equal(t, defaultContextLength, n)
}

func TestOllamaChat_Summarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Messages, 1)
		assert.True(t, strings.Contains(req.Messages[0].Content, "sample text"))

		line := chatStreamLine{Message: chatMessage{Content: "a summary"}, Done: true}
		b, _ := json.Marshal(line)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	c := NewOllamaChat(srv.URL, "test-model")
	got, err := c.Summarize(context.Background(), "sample text")
	require.NoError(t, err)
	assert.Equal(t, "a summary", got)
}
