package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/MadonnaMat/go-rag-lab/internal/chat/prompts"
)

// verify emits EventVerifying, then runs one extra non-streaming
// /api/chat call (no tools) asking the model to check its own draft. If
// the model responds "OK" (case-insensitive, trimmed), draftContent is
// returned unchanged. Otherwise its response is treated as a corrected
// final answer and EventRevised is emitted. A verify call error is
// non-fatal — falls back to the original draft rather than failing the
// whole request.
func (c *Chatter) verify(ctx context.Context, messages []chatMessage, draftContent string, emit func(Event) error) (string, error) {
	if err := emit(Event{Type: EventVerifying}); err != nil {
		return "", err
	}

	verifyMessages := append(append([]chatMessage{}, messages...), chatMessage{
		Role:    "user",
		Content: fmt.Sprintf(prompts.Verify, draftContent),
	})

	response, err := c.Client.chatOnce(ctx, verifyMessages)
	if err != nil {
		return draftContent, nil
	}

	response = strings.TrimSpace(response)
	if strings.EqualFold(response, "ok") || response == "" {
		return draftContent, nil
	}

	if err := emit(Event{Type: EventRevised, Revised: response}); err != nil {
		return "", err
	}
	return response, nil
}
