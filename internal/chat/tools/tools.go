// Package tools defines the tools the chat model can call, plus a registry
// for them. Each tool is self-contained and knows nothing about Ollama,
// HTTP, or SSE: internal/chat's dispatcher builds a Deps, looks a tool up
// by name, calls Run, and maps the returned Result to conversation
// messages and progress events. Adding a tool is one new file here plus
// one entry in All().
package tools

import (
	"context"
	"encoding/json"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// Def is a tool definition sent to the model, in Ollama's /api/chat
// "tools" wire format.
type Def struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// Call is one model-issued tool call, flattened from Ollama's wire shape.
type Call struct {
	ID   string
	Name string
	Args map[string]any
}

// Result is what a tool hands back. Exactly one of Err, Chunks, or
// (Summary+Payload) is meaningful:
//   - Err: a recoverable tool-level failure, fed back to the model as
//     {"error": ...} rather than aborting the request.
//   - Chunks: retrieval hits (retrieve_documents) — the UI renders the list.
//   - Summary+Payload: every other tool — Summary is a status-line one-liner,
//     Payload is the raw JSON for the UI to render.
//
// Content is the JSON string appended to the conversation as the
// role:"tool" message the model reads next (empty when Err is set).
type Result struct {
	Content string
	Summary string
	Payload json.RawMessage
	Chunks  []store.SearchResult
	Err     error
}

// RetrievedChunk is the model-facing JSON shape of one retrieval hit.
type RetrievedChunk struct {
	Source     string  `json:"source"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
	Score      float64 `json:"score"`
}

// errResult builds a failure Result.
func errResult(err error) Result { return Result{Err: err} }

// jsonResult marshals v as both the model-facing Content and the UI
// Payload, tagging it with a status-line summary.
func jsonResult(summary string, v any) Result {
	b, _ := json.Marshal(v)
	return Result{Content: string(b), Summary: summary, Payload: b}
}

// Retriever embeds a query and returns the best-matching chunks, using the
// given search mode (see store.SearchMode; "" means auto).
type Retriever interface {
	Query(ctx context.Context, q string, mode store.SearchMode, topK int) ([]store.SearchResult, error)
}

// DocLister lists every ingested document.
type DocLister interface {
	ListDocuments(ctx context.Context) ([]store.DocumentInfo, error)
}

// Loremaster re-ingests one just-written document by its bare filename.
type Loremaster interface {
	IngestFile(ctx context.Context, identity string, content []byte) (int, error)
}

// Deps is the set of capabilities the tools draw on. A nil capability
// disables the tools that need it (see each tool's Available).
type Deps struct {
	Retriever   Retriever
	DefaultTopK int
	Docs        DocLister
	Loremaster  Loremaster
	// LoreDir is the directory get_resource reads and lore_drop writes.
	LoreDir string
}

// Tool is one callable tool.
type Tool interface {
	Name() string
	Def() Def
	// Available reports whether deps has what this tool needs; unavailable
	// tools are neither advertised nor dispatched.
	Available(deps Deps) bool
	// Writes reports whether the tool mutates the corpus. Writing tools are
	// excluded from the post-answer verification pass.
	Writes() bool
	// OncePerTurn reports whether a second successful call in the same turn
	// should be refused (weak models loop on lore_drop).
	OncePerTurn() bool
	Run(ctx context.Context, call Call, deps Deps) Result
}

// All returns every registered tool.
func All() []Tool {
	return []Tool{retrieve{}, listResources{}, getResource{}, loreDrop{}}
}

// Available returns the tools usable with deps.
func Available(deps Deps) []Tool {
	out := make([]Tool, 0, len(All()))
	for _, t := range All() {
		if t.Available(deps) {
			out = append(out, t)
		}
	}
	return out
}

// Find returns the registered tool with the given name.
func Find(name string) (Tool, bool) {
	for _, t := range All() {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}
