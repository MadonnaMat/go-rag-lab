package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaEmbed_Success(t *testing.T) {
	var gotBody ollamaEmbedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/embed", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	got, err := o.Embed(context.Background(), []string{"first chunk", "second chunk"})
	require.NoError(t, err)

	assert.Equal(t, "nomic-embed-text", gotBody.Model)
	assert.Equal(t, []string{"first chunk", "second chunk"}, gotBody.Input)

	want := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
	assert.Equal(t, want, got)
}

func TestOllamaEmbed_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	_, err := o.Embed(context.Background(), []string{"text"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestOllamaEmbed_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	_, err := o.Embed(context.Background(), []string{"text"})
	require.Error(t, err)
}

func TestOllamaEmbed_EmptyInputMakesNoRequest(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	got, err := o.Embed(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.False(t, hit.Load(), "Embed made an HTTP request for empty input, want none")
}

func TestOllamaEmbed_CanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{0.1}}})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	o := NewOllama(srv.URL, "nomic-embed-text")
	_, err := o.Embed(ctx, []string{"text"})
	require.Error(t, err)
}
