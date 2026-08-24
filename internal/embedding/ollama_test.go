package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOllamaEmbed_Success(t *testing.T) {
	var gotBody ollamaEmbedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("request path = %q, want /api/embed", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	got, err := o.Embed(context.Background(), []string{"first chunk", "second chunk"})
	if err != nil {
		t.Fatalf("Embed returned unexpected error: %v", err)
	}

	if gotBody.Model != "nomic-embed-text" {
		t.Errorf("request model = %q, want nomic-embed-text", gotBody.Model)
	}
	if len(gotBody.Input) != 2 || gotBody.Input[0] != "first chunk" || gotBody.Input[1] != "second chunk" {
		t.Errorf("request input = %v, want [first chunk second chunk]", gotBody.Input)
	}

	want := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
	if len(got) != len(want) {
		t.Fatalf("got %d embeddings, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) || got[i][0] != want[i][0] || got[i][1] != want[i][1] {
			t.Errorf("embedding %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestOllamaEmbed_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	_, err := o.Embed(context.Background(), []string{"text"})
	if err == nil {
		t.Fatal("Embed returned nil error, want an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want it to mention the 500 status", err.Error())
	}
}

func TestOllamaEmbed_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	if _, err := o.Embed(context.Background(), []string{"text"}); err == nil {
		t.Fatal("Embed returned nil error, want an error for a malformed response body")
	}
}

func TestOllamaEmbed_EmptyInputMakesNoRequest(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text")
	got, err := o.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed returned unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Embed(nil) = %v, want nil", got)
	}
	if hit.Load() {
		t.Error("Embed made an HTTP request for empty input, want none")
	}
}

func TestOllamaEmbed_CanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{0.1}}})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	o := NewOllama(srv.URL, "nomic-embed-text")
	if _, err := o.Embed(ctx, []string{"text"}); err == nil {
		t.Fatal("Embed returned nil error, want an error for an already-canceled context")
	}
}
