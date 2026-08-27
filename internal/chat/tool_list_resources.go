package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// listResourcesToolName is the function name exposed to the model.
const listResourcesToolName = "list_resources"

// listResourcesToolDef describes the corpus-enumeration tool.
func listResourcesToolDef() toolDef {
	var t toolDef
	t.Type = "function"
	t.Function.Name = listResourcesToolName
	t.Function.Description = "List every document that has been ingested into the corpus, with its chunk count. Use this to answer \"what documents/topics do you have?\" and to see what exists before reading a whole document or adding new lore."
	t.Function.Parameters = map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	return t
}

// listResource is one entry in the list_resources result.
type listResource struct {
	Name   string `json:"name"`
	Chunks int    `json:"chunks"`
}

// runListResourcesTool returns every ingested document via c.Docs.
func (c *Chatter) runListResourcesTool(ctx context.Context, tc toolCall, emit func(Event) error) (chatMessage, error) {
	if err := emit(Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}); err != nil {
		return chatMessage{}, err
	}

	if c.Docs == nil {
		return toolErrorMessage(tc, emit, fmt.Errorf("listing resources is not available"))
	}

	docs, err := c.Docs.ListDocuments(ctx)
	if err != nil {
		return toolErrorMessage(tc, emit, err)
	}

	out := make([]listResource, len(docs))
	for i, d := range docs {
		out[i] = listResource{Name: d.Path, Chunks: d.Chunks}
	}
	payload, _ := json.Marshal(out)
	summary := fmt.Sprintf("%d document(s) in the corpus", len(out))
	return toolResultMessage(tc, emit, summary, string(payload))
}
