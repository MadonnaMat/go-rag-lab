package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// availableTools returns the tool definitions sent to Ollama on every
// turn. Add a new tool by writing its own tool_*.go file (see
// tool_retrieve.go) and listing its toolDef here plus a case in
// dispatchTool below.
//
// The disk/DB-backed resource tools are only advertised when their
// dependency is wired (see Chatter.Docs / Loremaster / LoreDir) — no point
// offering the model a tool that can only return an error.
func (c *Chatter) availableTools() []toolDef {
	tools := []toolDef{retrieveToolDef()}
	if c.Docs != nil {
		tools = append(tools, listResourcesToolDef())
	}
	if c.LoreDir != "" {
		tools = append(tools, getResourceToolDef())
	}
	if c.LoreDir != "" && c.Loremaster != nil {
		tools = append(tools, loreDropToolDef())
	}
	return tools
}

// dispatchTool executes a single tool call by name, returning the
// role:"tool" message to append to the conversation. Unknown tool names
// get a structured error message fed back to the model rather than
// failing the request.
func (c *Chatter) dispatchTool(ctx context.Context, prior []chatMessage, tc toolCall, emit func(Event) error) (chatMessage, error) {
	switch tc.Function.Name {
	case retrieveToolName:
		return c.runRetrieveTool(ctx, tc, emit)
	case listResourcesToolName:
		return c.runListResourcesTool(ctx, tc, emit)
	case getResourceToolName:
		return c.runGetResourceTool(ctx, tc, emit)
	case loreDropToolName:
		return c.runLoreDropTool(ctx, prior, tc, emit)
	default:
		if err := emit(Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}); err != nil {
			return chatMessage{}, err
		}
		toolErr := fmt.Errorf("unknown tool %q", tc.Function.Name)
		if err := emit(Event{Type: EventToolResult, Err: toolErr}); err != nil {
			return chatMessage{}, err
		}
		return chatMessage{
			Role:       "tool",
			ToolName:   tc.Function.Name,
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf(`{"error":%q}`, toolErr.Error()),
		}, nil
	}
}

// toolError emits the failure events and builds the role:"tool" message
// carrying a structured {"error": ...} back to the model — the shared
// shape every tool uses for a recoverable, tool-level failure (a Go error
// return is reserved for emit failures, which abort the whole request).
func toolErrorMessage(tc toolCall, emit func(Event) error, toolErr error) (chatMessage, error) {
	if err := emit(Event{Type: EventToolResult, Err: toolErr}); err != nil {
		return chatMessage{}, err
	}
	return chatMessage{
		Role:       "tool",
		ToolName:   tc.Function.Name,
		ToolCallID: tc.ID,
		Content:    fmt.Sprintf(`{"error":%q}`, toolErr.Error()),
	}, nil
}

// toolResultMessage emits EventToolResult with a human-readable summary
// and builds the role:"tool" message carrying payload (JSON-marshaled) to
// the model.
func toolResultMessage(tc toolCall, emit func(Event) error, summary string, payload string) (chatMessage, error) {
	if err := emit(Event{Type: EventToolResult, ToolSummary: summary, ToolPayload: json.RawMessage(payload)}); err != nil {
		return chatMessage{}, err
	}
	return chatMessage{
		Role:       "tool",
		ToolName:   tc.Function.Name,
		ToolCallID: tc.ID,
		Content:    payload,
	}, nil
}
