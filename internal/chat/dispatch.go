package chat

import (
	"context"
	"fmt"

	"github.com/MadonnaMat/go-rag-lab/internal/chat/tools"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// toolResultChunk is the retrieval payload carried on an EventToolResult
// to the SSE layer, mirroring store.SearchResult.
type toolResultChunk struct {
	Source     string  `json:"source"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
}

func toResultChunks(results []store.SearchResult) []toolResultChunk {
	out := make([]toolResultChunk, len(results))
	for i, r := range results {
		out[i] = toolResultChunk{Source: r.Source, ChunkIndex: r.ChunkIndex, Content: r.Content, Distance: r.Distance}
	}
	return out
}

// toolDeps snapshots the Chatter's capabilities for the tools package.
func (c *Chatter) toolDeps() tools.Deps {
	return tools.Deps{
		Retriever:   c.Retriever,
		DefaultTopK: c.DefaultTopK,
		Docs:        c.Docs,
		Loremaster:  c.Loremaster,
		LoreDir:     c.LoreDir,
	}
}

// toolDefs returns the tool definitions to advertise to the model. When
// readOnly is set, tools that write to the corpus are dropped — the
// verification pass may fact-check against the corpus but must never
// mutate it.
func (c *Chatter) toolDefs(readOnly bool) []tools.Def {
	deps := c.toolDeps()
	var out []tools.Def
	for _, t := range tools.Available(deps) {
		if readOnly && t.Writes() {
			continue
		}
		out = append(out, t.Def())
	}
	return out
}

// executeToolCalls dispatches each model-issued tool call in order,
// returning the role:"tool" messages to append to the conversation.
// succeeded tracks once-per-turn tools that have already run in this Run,
// so a repeat call is refused rather than executed. When readOnly is set
// (the verification pass), any tool that writes to the corpus is refused
// outright — not merely left unadvertised.
// retrieved, when non-nil, accumulates every chunk returned by a retrieval
// tool call — the caller (runLoop) uses it to work out which sources
// informed the final answer. The verify pass passes nil.
func (c *Chatter) executeToolCalls(ctx context.Context, calls []toolCall, succeeded map[string]bool, readOnly bool, retrieved *[]store.SearchResult, emit func(Event) error) ([]chatMessage, error) {
	deps := c.toolDeps()
	out := make([]chatMessage, 0, len(calls))
	for _, wire := range calls {
		call := tools.Call{ID: wire.ID, Name: wire.Function.Name, Args: wire.Function.Arguments}

		if err := emit(Event{Type: EventToolCall, ToolName: call.Name, ToolArgs: call.Args}); err != nil {
			return nil, err
		}

		res := runTool(ctx, deps, call, succeeded, readOnly)
		if retrieved != nil && res.Err == nil && len(res.Chunks) > 0 {
			*retrieved = append(*retrieved, res.Chunks...)
		}
		msg, err := c.emitToolResult(call, res, emit)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// runTool resolves a call to a registered, available tool and runs it,
// applying the read-only and once-per-turn guards. It never returns a Go
// error — a failure is a Result.Err, fed back to the model.
func runTool(ctx context.Context, deps tools.Deps, call tools.Call, succeeded map[string]bool, readOnly bool) tools.Result {
	tool, ok := tools.Find(call.Name)
	if !ok || !tool.Available(deps) {
		return tools.Result{Err: fmt.Errorf("unknown tool %q", call.Name)}
	}
	if readOnly && tool.Writes() {
		return tools.Result{Err: fmt.Errorf("%s writes to the corpus and cannot be used here — this is a read-only step", call.Name)}
	}
	if tool.OncePerTurn() && succeeded[call.Name] {
		return tools.Result{Err: fmt.Errorf("%s was already used in this turn — do not call it again; answer the user now using what you have", call.Name)}
	}

	res := tool.Run(ctx, call, deps)
	if res.Err == nil && tool.OncePerTurn() {
		succeeded[call.Name] = true
	}
	return res
}

// emitToolResult emits the EventToolResult for res and builds the
// role:"tool" message to append to the conversation.
func (c *Chatter) emitToolResult(call tools.Call, res tools.Result, emit func(Event) error) (chatMessage, error) {
	if res.Err != nil {
		if err := emit(Event{Type: EventToolResult, Err: res.Err}); err != nil {
			return chatMessage{}, err
		}
		return toolMessage(call, fmt.Sprintf(`{"error":%q}`, res.Err.Error())), nil
	}

	ev := Event{Type: EventToolResult}
	if res.Summary != "" {
		ev.ToolSummary, ev.ToolPayload = res.Summary, res.Payload
	} else {
		ev.ToolResult = toResultChunks(res.Chunks)
	}
	if err := emit(ev); err != nil {
		return chatMessage{}, err
	}
	return toolMessage(call, res.Content), nil
}

func toolMessage(call tools.Call, content string) chatMessage {
	return chatMessage{Role: "tool", ToolName: call.Name, ToolCallID: call.ID, Content: content}
}
