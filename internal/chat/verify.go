package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/MadonnaMat/go-rag-lab/internal/chat/prompts"
)

// verifyMaxIterations bounds the verify tool-call loop, like
// MaxToolIterations does for the main loop.
const verifyMaxIterations = 3

// verifyTools is availableTools minus lore_drop: the self-check pass may
// read the corpus (retrieve_documents / list_resources / get_resource) to
// fact-check its own draft, but must never write to it.
func (c *Chatter) verifyTools() []toolDef {
	all := c.availableTools()
	out := make([]toolDef, 0, len(all))
	for _, t := range all {
		if t.Function.Name != loreDropToolName {
			out = append(out, t)
		}
	}
	return out
}

// verify emits EventVerifying, then runs a short bounded tool-call loop
// asking the model to check its own draft — it may call the read-only
// corpus tools to do so (emitting the usual EventToolCall/EventToolResult
// as it goes). If the final response is "OK" (case-insensitive, trimmed),
// draftContent is returned unchanged. Otherwise the response is treated as
// a corrected final answer and EventRevised is emitted. A verify call
// error is non-fatal — falls back to the original draft rather than
// failing the whole request.
func (c *Chatter) verify(ctx context.Context, messages []chatMessage, draftContent string, emit func(Event) error) (string, error) {
	if err := emit(Event{Type: EventVerifying}); err != nil {
		return "", err
	}

	msgs := append(append([]chatMessage{}, messages...), chatMessage{
		Role:    "user",
		Content: fmt.Sprintf(prompts.Verify, draftContent),
	})

	for iteration := 0; iteration < verifyMaxIterations; iteration++ {
		var draft chatMessage
		var sawToolCall bool

		err := c.Client.Chat(ctx, msgs, c.verifyTools(), func(line chatStreamLine) error {
			if len(line.Message.ToolCalls) > 0 {
				sawToolCall = true
				draft.ToolCalls = append(draft.ToolCalls, line.Message.ToolCalls...)
			}
			if line.Message.Content != "" && !sawToolCall {
				draft.Content += line.Message.Content
			}
			return nil
		})
		if err != nil {
			return draftContent, nil
		}

		if !sawToolCall {
			response := strings.TrimSpace(draft.Content)
			if strings.EqualFold(response, "ok") || response == "" {
				return draftContent, nil
			}
			if err := emit(Event{Type: EventRevised, Revised: response}); err != nil {
				return "", err
			}
			return response, nil
		}

		msgs = append(msgs, draft)
		toolMessages, err := c.executeToolCalls(ctx, draft.ToolCalls, emit)
		if err != nil {
			return "", err
		}
		msgs = append(msgs, toolMessages...)
	}

	// Model kept calling tools without settling — keep the original draft.
	return draftContent, nil
}
