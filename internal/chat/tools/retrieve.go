package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// retrieve is the semantic chunk-search tool.
type retrieve struct{}

func (retrieve) Name() string        { return "retrieve_documents" }
func (retrieve) Available(Deps) bool { return true }
func (retrieve) Writes() bool        { return false }
func (retrieve) OncePerTurn() bool   { return false }

func (retrieve) Def() Def {
	var d Def
	d.Type = "function"
	d.Function.Name = "retrieve_documents"
	d.Function.Description = "Search the ingested document corpus for chunks relevant to a query. Use this whenever answering requires specific facts from the documents rather than general knowledge."
	d.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to embed and match against document chunks.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Number of chunks to return (optional).",
			},
		},
		"required": []string{"query"},
	}
	return d
}

func (retrieve) Run(ctx context.Context, call Call, deps Deps) Result {
	topK := deps.DefaultTopK
	if v, ok := call.Args["top_k"].(float64); ok && v > 0 {
		topK = int(v)
	}

	query, ok := call.Args["query"].(string)
	if !ok || query == "" {
		return errResult(fmt.Errorf("missing or invalid required argument %q", "query"))
	}

	results, err := deps.Retriever.Query(ctx, query, topK)
	if err != nil {
		return errResult(err)
	}

	chunks := make([]RetrievedChunk, len(results))
	for i, r := range results {
		chunks[i] = RetrievedChunk{Source: r.Source, Content: r.Content, Distance: r.Distance}
	}
	payload, _ := json.Marshal(chunks)
	return Result{Content: string(payload), Chunks: results}
}
