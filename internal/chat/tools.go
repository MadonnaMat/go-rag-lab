package chat

import (
	"context"
	"fmt"
)

// availableTools returns the tool definitions sent to Ollama on every
// turn. Add a new tool by writing its own tool_*.go file (see
// tool_retrieve.go) and listing its toolDef here plus a case in
// dispatchTool below.
func availableTools() []toolDef {
	return []toolDef{retrieveToolDef()}
}

// dispatchTool executes a single tool call by name, returning the
// role:"tool" message to append to the conversation. Unknown tool names
// get a structured error message fed back to the model rather than
// failing the request.
func (c *Chatter) dispatchTool(ctx context.Context, tc toolCall, emit func(Event) error) (chatMessage, error) {
	switch tc.Function.Name {
	case retrieveToolName:
		return c.runRetrieveTool(ctx, tc, emit)
	default:
		return chatMessage{
			Role:       "tool",
			ToolName:   tc.Function.Name,
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf(`{"error":"unknown tool %q"}`, tc.Function.Name),
		}, nil
	}
}
