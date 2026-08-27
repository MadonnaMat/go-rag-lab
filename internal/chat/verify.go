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
	succeeded := map[string]bool{}

	for iteration := 0; iteration < verifyMaxIterations; iteration++ {
		draft, sawToolCall, err := c.verifyTurn(ctx, msgs)
		if err != nil {
			return draftContent, nil
		}

		if !sawToolCall {
			return c.applyVerifyVerdict(draftContent, draft.Content, emit)
		}

		msgs = append(msgs, draft)
		toolMessages, err := c.executeToolCalls(ctx, draft.ToolCalls, succeeded, true, emit)
		if err != nil {
			return "", err
		}
		msgs = append(msgs, toolMessages...)
	}

	// Model kept calling tools without settling — keep the original draft.
	return draftContent, nil
}

// verifyTurn runs one non-emitting model turn for the verify loop,
// accumulating content and any tool calls.
func (c *Chatter) verifyTurn(ctx context.Context, msgs []chatMessage) (chatMessage, bool, error) {
	var draft chatMessage
	var sawToolCall bool
	err := c.Client.Chat(ctx, msgs, c.toolDefs(true), func(line chatStreamLine) error {
		if len(line.Message.ToolCalls) > 0 {
			sawToolCall = true
			draft.ToolCalls = append(draft.ToolCalls, line.Message.ToolCalls...)
		}
		if line.Message.Content != "" && !sawToolCall {
			draft.Content += line.Message.Content
		}
		return nil
	})
	return draft, sawToolCall, err
}

// applyVerifyVerdict interprets the model's final verify response: "OK"
// (or empty) keeps the draft, anything else is a correction.
func (c *Chatter) applyVerifyVerdict(draftContent, response string, emit func(Event) error) (string, error) {
	response = strings.TrimSpace(response)
	if strings.EqualFold(response, "ok") || response == "" {
		return draftContent, nil
	}
	if err := emit(Event{Type: EventRevised, Revised: response}); err != nil {
		return "", err
	}
	return response, nil
}
