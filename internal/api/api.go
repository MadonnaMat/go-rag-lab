// Package api is the HTTP layer over internal/retrieve: a thin chi router
// that parses a query from the request, calls a Retriever, and encodes the
// results as JSON — no orchestration logic of its own, the query-side
// mirror of how cmd/ingest is a thin wrapper around internal/ingest.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

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
	r.Get("/query", h.handleQuery)
	r.Get("/swagger/*", httpSwagger.WrapHandler)
	return r
}

// QueryResult is one ranked chunk in a QueryResponse.
type QueryResult struct {
	Source   string  `json:"source"`
	Content  string  `json:"content"`
	Distance float64 `json:"distance"`
}

// QueryResponse is the GET /query response body.
type QueryResponse struct {
	Results []QueryResult `json:"results"`
}

// handleQuery godoc
//
//	@Summary		Search ingested chunks
//	@Description	Embeds the query text with the same provider used at ingestion time, then returns the topK nearest chunks by cosine distance (nearest first). A search like this is a safe, idempotent read, so it's a GET with query parameters rather than a POST with a body.
//	@Tags			query
//	@Produce		json
//	@Param			query	query		string	true	"Query text"
//	@Param			top_k	query		int		false	"Number of results to return (defaults to the server's configured default)"
//	@Success		200		{object}	QueryResponse
//	@Failure		400		{object}	map[string]string
//	@Failure		500		{object}	map[string]string
//	@Router			/query [get]
func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query must not be empty")
		return
	}

	topK := h.DefaultTopK
	if raw := r.URL.Query().Get("top_k"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "top_k must be an integer")
			return
		}
		if n > 0 {
			topK = n
		}
	}

	results, err := h.Retriever.Query(r.Context(), q, topK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := QueryResponse{Results: make([]QueryResult, len(results))}
	for i, res := range results {
		resp.Results[i] = QueryResult{Source: res.Source, Content: res.Content, Distance: res.Distance}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleHealthz godoc
//
//	@Summary	Health check
//	@Tags		health
//	@Success	200
//	@Router		/healthz [get]
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
