package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MadonnaMat/go-rag-lab/internal/chat"
)

// sseEncoder writes chat.Events as Server-Sent Events frames, flushing
// after each one so a client sees them as they happen rather than
// buffered until the response completes.
type sseEncoder struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

type sseToolCallPayload struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

type sseToolResultChunk struct {
	Source   string  `json:"source"`
	Content  string  `json:"content"`
	Distance float64 `json:"distance"`
}

type sseToolResultPayload struct {
	Results []sseToolResultChunk `json:"results"`
	// Error is set instead of Results when the tool call itself failed
	// (e.g. the retriever errored) — distinguishes "the search legitimately
	// found nothing" from "the search couldn't run".
	Error string `json:"error,omitempty"`
	// Message is set instead of Results for the non-retrieval tools
	// (list_resources / get_resource / lore_drop): a human-readable
	// one-liner for the UI status line.
	Message string `json:"message,omitempty"`
}

type sseThinkingPayload struct {
	Content string `json:"content"`
}

type sseTokenPayload struct {
	Content string `json:"content"`
}

type sseCompactedPayload struct {
	Summary string `json:"summary"`
}

type sseRevisedPayload struct {
	Content string `json:"content"`
}

type sseContextUsagePayload struct {
	UsedTokens    int `json:"used_tokens"`
	ContextTokens int `json:"context_tokens"`
}

type sseErrorPayload struct {
	Error string `json:"error"`
}

// write maps a chat.Event to its named SSE event and flushes it.
func (e *sseEncoder) write(ev chat.Event) error {
	switch ev.Type {
	case chat.EventToolCall:
		return e.frame("tool_call", sseToolCallPayload{Tool: ev.ToolName, Args: ev.ToolArgs})
	case chat.EventToolResult:
		if ev.Err != nil {
			return e.frame("tool_result", sseToolResultPayload{Error: ev.Err.Error()})
		}
		if ev.ToolSummary != "" {
			return e.frame("tool_result", sseToolResultPayload{Message: ev.ToolSummary})
		}
		results := make([]sseToolResultChunk, len(ev.ToolResult))
		for i, r := range ev.ToolResult {
			results[i] = sseToolResultChunk{Source: r.Source, Content: r.Content, Distance: r.Distance}
		}
		return e.frame("tool_result", sseToolResultPayload{Results: results})
	case chat.EventThinking:
		return e.frame("thinking", sseThinkingPayload{Content: ev.Token})
	case chat.EventToken:
		return e.frame("token", sseTokenPayload{Content: ev.Token})
	case chat.EventCompacting:
		return e.frame("compacting", struct{}{})
	case chat.EventCompacted:
		return e.frame("compacted", sseCompactedPayload{Summary: ev.Summary})
	case chat.EventVerifying:
		return e.frame("verifying", struct{}{})
	case chat.EventRevised:
		return e.frame("revised", sseRevisedPayload{Content: ev.Revised})
	case chat.EventContextUsage:
		return e.frame("context_usage", sseContextUsagePayload{UsedTokens: ev.UsedTokens, ContextTokens: ev.ContextTokens})
	case chat.EventDone:
		return e.frame("done", struct{}{})
	case chat.EventError:
		msg := "unknown error"
		if ev.Err != nil {
			msg = ev.Err.Error()
		}
		return e.frame("error", sseErrorPayload{Error: msg})
	}
	return nil
}

func (e *sseEncoder) writeError(err error) error {
	return e.frame("error", sseErrorPayload{Error: err.Error()})
}

func (e *sseEncoder) frame(event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	e.flusher.Flush()
	return nil
}
