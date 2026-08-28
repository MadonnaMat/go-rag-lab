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
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/MadonnaMat/go-rag-lab/internal/chat"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// Retriever is the subset of *retrieve.Retriever the HTTP layer needs.
// Defining it here (rather than depending on the concrete type) lets tests
// substitute a fake — same reasoning as retrieve.Store.
type Retriever interface {
	Query(ctx context.Context, q string, mode store.SearchMode, topK int) ([]store.SearchResult, error)
}

// Chatter is the subset of *chat.Chatter the HTTP layer needs.
type Chatter interface {
	Run(ctx context.Context, history []chat.Message, emit func(chat.Event) error) error
}

// Handler holds the HTTP layer's dependencies.
type Handler struct {
	Retriever Retriever
	Chatter   Chatter
	// DefaultTopK is used when a request omits top_k (or sets it <= 0).
	DefaultTopK int
	// LoreDir is the directory GET /lore/{name} reads .md files from.
	LoreDir string
	// LoreChunks supplies the ingested text of cited chunks so /lore can
	// highlight them; optional (nil disables highlighting).
	LoreChunks LoreChunkSource
}

// NewRouter builds the HTTP routes backed by h.
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Get("/healthz", h.handleHealthz)
	r.Get("/query", h.handleQuery)
	r.Get("/lore/{name}", h.handleLore)
	r.Post("/chat", h.handleChat)
	r.Get("/", h.handleChatPage)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	r.Get("/swagger/*", httpSwagger.WrapHandler)
	return r
}

// QueryResult is one ranked chunk in a QueryResponse.
type QueryResult struct {
	Source     string `json:"source"`
	ChunkIndex int    `json:"chunk_index"`
	Content    string `json:"content"`
	// Distance is pgvector cosine distance — meaningful for mode=vector and
	// for a chunk that ranked on the vector side under mode=auto; 0 for a
	// keyword-only hit.
	Distance float64 `json:"distance"`
	// Score is the ranking score actually used: RRF for mode=auto, ts_rank
	// for mode=keyword, 0 for mode=vector (order by Distance there). Higher
	// is better. This is the field to sort/threshold on.
	Score float64 `json:"score"`
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
//	@Param			mode	query		string	false	"Ranking mode: auto (default, hybrid), vector, or keyword"
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

	mode := store.SearchMode(r.URL.Query().Get("mode"))
	switch mode {
	case "", store.SearchAuto, store.SearchVector, store.SearchKeyword:
	default:
		writeError(w, http.StatusBadRequest, "mode must be auto, vector, or keyword")
		return
	}

	results, err := h.Retriever.Query(r.Context(), q, mode, topK)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := QueryResponse{Results: make([]QueryResult, len(results))}
	for i, res := range results {
		resp.Results[i] = QueryResult{Source: res.Source, ChunkIndex: res.ChunkIndex, Content: res.Content, Distance: res.Distance, Score: res.Score}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ChatMessage is one turn in a client-supplied conversation history.
type ChatMessage struct {
	Role    string `json:"role"` // "user" or "assistant" only — a "system" role is rejected
	Content string `json:"content"`
}

// ChatRequest is the POST /chat request body — the full conversation so
// far. Chat is stateless: the server holds no session state, so the
// client resends the whole history each request.
type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
}

// handleChat godoc
//
//	@Summary		Tool-calling RAG chat
//	@Description	Streams a chat response over Server-Sent Events. The model may call a document-retrieval tool zero or more times before producing a final answer. Event types: tool_call, tool_result, thinking, token, compacted, verifying, revised, context_usage, sources, done, error. Swag/OpenAPI 2.0 can't represent an SSE event stream's per-event payloads, so only the request body is documented here.
//	@Tags			chat
//	@Accept			json
//	@Produce		text/event-stream
//	@Param			request	body		ChatRequest	true	"Conversation history"
//	@Success		200		{string}	string		"text/event-stream"
//	@Failure		400		{object}	map[string]string
//	@Router			/chat [post]
func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages must not be empty")
		return
	}
	for _, m := range req.Messages {
		if strings.EqualFold(m.Role, "system") {
			writeError(w, http.StatusBadRequest, "system-role messages are not accepted from clients")
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	history := make([]chat.Message, len(req.Messages))
	for i, m := range req.Messages {
		history[i] = chat.Message{Role: m.Role, Content: m.Content}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	enc := sseEncoder{w: w, flusher: flusher}
	err := h.Chatter.Run(r.Context(), history, enc.write)
	if err != nil && r.Context().Err() == nil {
		_ = enc.writeError(err)
	}
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
