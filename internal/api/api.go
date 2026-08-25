// Package api is the HTTP layer over internal/retrieve: a thin chi router
// that decodes a query request, calls a Retriever, and encodes the results
// as JSON — no orchestration logic of its own, the query-side mirror of how
// cmd/ingest is a thin wrapper around internal/ingest.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// Retriever is the subset of *retrieve.Retriever the HTTP layer needs.
// Defining it here (rather than depending on the concrete type) lets tests
// substitute a fake — same reasoning as retrieve.Store.
type Retriever interface {
	Query(ctx context.Context, q string, topK int) ([]store.SearchResult, error)
}

// Handler holds the HTTP layer's dependencies.
type Handler struct {
	Retriever Retriever
	// DefaultTopK is used when a request omits top_k (or sets it <= 0).
	DefaultTopK int
}

// NewRouter builds the HTTP routes backed by h.
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Get("/healthz", h.handleHealthz)
	r.Post("/query", h.handleQuery)
	return r
}

type queryRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

type queryResult struct {
	Source   string  `json:"source"`
	Content  string  `json:"content"`
	Distance float64 `json:"distance"`
}

type queryResponse struct {
	Results []queryResult `json:"results"`
}

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query must not be empty")
		return
	}

	topK := req.TopK
	if topK <= 0 {
		topK = h.DefaultTopK
	}

	results, err := h.Retriever.Query(r.Context(), req.Query, topK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := queryResponse{Results: make([]queryResult, len(results))}
	for i, res := range results {
		resp.Results[i] = queryResult{Source: res.Source, Content: res.Content, Distance: res.Distance}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
