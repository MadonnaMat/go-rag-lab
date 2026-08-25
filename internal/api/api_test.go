package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// fakeRetriever records what it was asked and returns a fixed
// results/error, so tests never need a real Retriever or Postgres.
type fakeRetriever struct {
	gotQuery string
	gotTopK  int
	called   bool
	results  []store.SearchResult
	err      error
}

func (f *fakeRetriever) Query(ctx context.Context, q string, topK int) ([]store.SearchResult, error) {
	f.called = true
	f.gotQuery = q
	f.gotTopK = topK
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func TestHandleQuery(t *testing.T) {
	tests := []struct {
		name             string
		body             string
		retriever        *fakeRetriever
		wantStatus       int
		wantBodyContains string
		wantCalled       bool
		wantTopK         int
	}{
		{
			name:             "happy path",
			body:             `{"query":"how does X work","top_k":2}`,
			retriever:        &fakeRetriever{results: []store.SearchResult{{Source: "a.md", Content: "hello", Distance: 0.1}}},
			wantStatus:       http.StatusOK,
			wantBodyContains: `"source":"a.md"`,
			wantCalled:       true,
			wantTopK:         2,
		},
		{
			name:             "empty query is rejected before calling the retriever",
			body:             `{"query":"","top_k":2}`,
			retriever:        &fakeRetriever{},
			wantStatus:       http.StatusBadRequest,
			wantBodyContains: "query must not be empty",
			wantCalled:       false,
		},
		{
			name:             "malformed JSON is rejected before calling the retriever",
			body:             `{not json`,
			retriever:        &fakeRetriever{},
			wantStatus:       http.StatusBadRequest,
			wantBodyContains: "invalid JSON body",
			wantCalled:       false,
		},
		{
			name:             "retriever error surfaces as 500",
			body:             `{"query":"how does X work","top_k":2}`,
			retriever:        &fakeRetriever{err: errors.New("embedding backend unavailable")},
			wantStatus:       http.StatusInternalServerError,
			wantBodyContains: "embedding backend unavailable",
			wantCalled:       true,
			wantTopK:         2,
		},
		{
			name:             "top_k omitted falls back to DefaultTopK",
			body:             `{"query":"how does X work"}`,
			retriever:        &fakeRetriever{},
			wantStatus:       http.StatusOK,
			wantBodyContains: `"results":[]`,
			wantCalled:       true,
			wantTopK:         5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{Retriever: tt.retriever, DefaultTopK: 5}
			router := NewRouter(h)

			req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBodyContains)
			assert.Equal(t, tt.wantCalled, tt.retriever.called)
			if tt.wantCalled {
				assert.Equal(t, tt.wantTopK, tt.retriever.gotTopK)
			}
		})
	}
}

func TestHandleHealthz(t *testing.T) {
	router := NewRouter(&Handler{Retriever: &fakeRetriever{}, DefaultTopK: 5})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
