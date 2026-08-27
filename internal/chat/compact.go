package chat

import (
	"context"
	"fmt"

	"github.com/MadonnaMat/go-rag-lab/internal/chat/prompts"
)

// compactThreshold is the fraction of the model's context window at which
// auto-compaction kicks in, leaving headroom for the tool schema, the
// system prompt, and the model's own response.
const compactThreshold = 0.8

// keepRecent is how many of the most recent messages are always left
// untouched by compaction.
const keepRecent = 4

// estimateTokens is a rough, tokenizer-free heuristic (chars/4) — no
// tokenizer is vendored, so this only needs to be right enough to trigger
// compaction before a real overflow, not exact.
func estimateTokens(messages []chatMessage) int {
	chars := 0
	for _, m := range messages {
		chars += len(m.Content) + len(m.Thinking)
	}
	return chars / 4
}

// compact summarizes the older portion of messages (everything between the
// leading system message and the most recent keepRecent messages) via a
// dedicated non-streaming /api/chat call (no tools attached), and splices
// the result back in as a single synthetic message immediately after the
// system prompt. If there's nothing meaningful to compact (too few
// messages), it returns messages unchanged and an empty summary — callers
// treat an empty summary as "nothing happened." If the summarization call
// itself fails, it falls back to hard-dropping the oldest messages rather
// than erroring the whole request.
func (c *Chatter) compact(ctx context.Context, messages []chatMessage) ([]chatMessage, string) {
	if len(messages) == 0 || messages[0].Role != "system" {
		return messages, ""
	}

	head := messages[:1]
	rest := messages[1:]
	if len(rest) <= keepRecent {
		return messages, ""
	}

	// Split at a message boundary that doesn't separate an assistant
	// tool_calls message from its role:"tool" responses — Ollama's
	// /api/chat rejects a "tool" message with no preceding tool_calls in
	// context. Walk the split point back over any run of "tool" messages
	// so the whole call/response group stays together in recent.
	splitAt := len(rest) - keepRecent
	for splitAt > 0 && rest[splitAt].Role == "tool" {
		splitAt--
	}
	older := rest[:splitAt]
	recent := rest[splitAt:]

	summarizeMessages := append([]chatMessage{{Role: "system", Content: prompts.Compact}}, older...)
	summary, err := c.Client.chatOnce(ctx, summarizeMessages)
	if err != nil {
		dropped := splitAt
		out := append([]chatMessage{}, head...)
		out = append(out, recent...)
		return out, fmt.Sprintf("dropped %d older message(s) (summarization failed: %v)", dropped, err)
	}

	summaryMsg := chatMessage{Role: "system", Content: "Summary of earlier conversation: " + summary}
	out := append([]chatMessage{}, head...)
	out = append(out, summaryMsg)
	out = append(out, recent...)
	return out, summary
}
