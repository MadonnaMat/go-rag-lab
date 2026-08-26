package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// retrieveToolName is the function name exposed to the model.
const retrieveToolName = "retrieve_documents"

// retrieveToolDef describes the retrieval tool the model can call.
func retrieveToolDef() toolDef {
	var t toolDef
	t.Type = "function"
	t.Function.Name = retrieveToolName
	t.Function.Description = "Search the ingested document corpus for chunks relevant to a query. Use this whenever answering requires specific facts from the documents rather than general knowledge."
	t.Function.Parameters = map[string]any{
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
	return t
}

// toolResultChunk is what a retrieve_documents call hands back to the
// model, mirroring store.SearchResult.
type toolResultChunk struct {
	Source   string  `json:"source"`
	Content  string  `json:"content"`
	Distance float64 `json:"distance"`
}

// runRetrieveTool executes one retrieve_documents call: embeds+searches
// via c.Retriever, emits EventToolCall/EventToolResult, and returns the
// role:"tool" message to append to the conversation. A Retriever error is
// fed back to the model as a structured error rather than failing the
// whole request.
func (c *Chatter) runRetrieveTool(ctx context.Context, tc toolCall, emit func(Event) error) (chatMessage, error) {
	query, _ := tc.Function.Arguments["query"].(string)
	topK := c.DefaultTopK
	if v, ok := tc.Function.Arguments["top_k"].(float64); ok && v > 0 {
		topK = int(v)
	}

	if err := emit(Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}); err != nil {
		return chatMessage{}, err
	}

	results, err := c.Retriever.Query(ctx, query, topK)
	if err != nil {
		if emitErr := emit(Event{Type: EventToolResult, ToolResult: nil}); emitErr != nil {
			return chatMessage{}, emitErr
		}
		return chatMessage{
			Role:     "tool",
			ToolName: tc.Function.Name,
			Content:  fmt.Sprintf(`{"error":%q}`, err.Error()),
		}, nil
	}

	chunks := toResultChunks(results)
	if err := emit(Event{Type: EventToolResult, ToolResult: chunks}); err != nil {
		return chatMessage{}, err
	}

	payload, _ := json.Marshal(chunks)
	return chatMessage{Role: "tool", ToolName: tc.Function.Name, Content: string(payload)}, nil
}

func toResultChunks(results []store.SearchResult) []toolResultChunk {
	out := make([]toolResultChunk, len(results))
	for i, r := range results {
		out[i] = toolResultChunk{Source: r.Source, Content: r.Content, Distance: r.Distance}
	}
	return out
}
