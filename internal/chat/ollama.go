// Package chat orchestrates a tool-calling RAG chat conversation against
// Ollama's /api/chat: it owns the hand-rolled streaming HTTP client, the
// retrieval tool the model can call, auto-compaction of long
// conversations, and a post-answer self-verification pass. It knows
// nothing about HTTP/SSE — internal/api is a thin layer on top, the same
// relationship internal/api already has with internal/retrieve.
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/MadonnaMat/go-rag-lab/internal/chat/prompts"
)

// defaultContextLength is used when Ollama's /api/show doesn't return a
// usable context_length for the model (missing field, unparseable, or the
// request itself fails).
const defaultContextLength = 4096

// OllamaChat is a hand-rolled client for Ollama's streaming /api/chat and
// /api/show endpoints — same conventions as internal/embedding/ollama.go
// (private wire types, http.NewRequestWithContext, status-check-with-body,
// json decoding), no client library.
type OllamaChat struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewOllamaChat constructs a chat client. baseURL is Ollama's HTTP address
// (e.g. "http://localhost:11434"); model is the chat model name (e.g.
// "qwen3:8b").
func NewOllamaChat(baseURL, model string) *OllamaChat {
	return &OllamaChat{
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{},
	}
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// toolCall mirrors Ollama's wire format for a model-issued tool call —
// confirmed against a real /api/chat response: {"id":"call_...",
// "function":{"index":0,"name":"...","arguments":{...}}}. Index isn't
// captured since nothing here needs it.
type toolCall struct {
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type toolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []toolDef     `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	Think    bool          `json:"think,omitempty"`
}

type chatStreamLine struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
}

// Chat sends a /api/chat request with Stream:true and Think:true on the
// wire (one code path for both streaming and non-streaming callers — a
// non-streaming caller just aggregates the lines itself), decoding each
// NDJSON response line via bufio.Scanner and invoking onLine in order
// until Done:true or ctx is canceled. A line's Message.Thinking and
// Message.Content are populated independently as the model alternates
// between reasoning and answering.
func (c *OllamaChat) Chat(ctx context.Context, messages []chatMessage, tools []toolDef, onLine func(chatStreamLine) error) error {
	reqBody, err := json.Marshal(chatRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
		Think:    true,
	})
	if err != nil {
		return fmt.Errorf("marshal ollama chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build ollama chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama chat request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama chat request failed: %s: %s", resp.Status, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	// A tool-result message embedded back into the conversation can be
	// long (JSON-encoded retrieved chunks); bump well past the 64KB
	// default so a single NDJSON line can't overflow it.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var parsed chatStreamLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			return fmt.Errorf("decode ollama chat stream line: %w", err)
		}
		if err := onLine(parsed); err != nil {
			return err
		}
		if parsed.Done {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ollama chat stream: %w", err)
	}
	return nil
}

// chatOnce runs a single tool-free Chat call and aggregates the streamed
// Content deltas into one string — the shared shape behind every
// one-shot, answer-is-just-text call this package makes (corpus
// summarization, conversation compaction, answer verification), so each
// of those only needs to build its own messages and prompt text.
func (c *OllamaChat) chatOnce(ctx context.Context, messages []chatMessage) (string, error) {
	var out string
	err := c.Chat(ctx, messages, nil, func(line chatStreamLine) error {
		out += line.Message.Content
		return nil
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// Summarize sends a single non-streaming, tool-free /api/chat call built
// from prompts.CorpusSummary and sample, returning the model's response
// text. Used by cmd/ingest to generate a one-time corpus description —
// satisfies ingest.Summarizer structurally.
func (c *OllamaChat) Summarize(ctx context.Context, sample string) (string, error) {
	messages := []chatMessage{{Role: "user", Content: fmt.Sprintf(prompts.CorpusSummary, sample)}}
	return c.chatOnce(ctx, messages)
}

type ollamaShowRequest struct {
	Model string `json:"model"`
}

type ollamaShowResponse struct {
	ModelInfo map[string]any `json:"model_info"`
}

// ContextLength queries Ollama's /api/show for c.model and returns its
// context window size in tokens. The field lives in model_info under a
// key whose prefix is the model's architecture (e.g.
// "qwen3.context_length"), so this scans model_info's keys for one ending
// in ".context_length" rather than hardcoding the architecture. Falls
// back to defaultContextLength if the request fails or the field can't be
// found/parsed.
func (c *OllamaChat) ContextLength(ctx context.Context) (int, error) {
	reqBody, err := json.Marshal(ollamaShowRequest{Model: c.model})
	if err != nil {
		return defaultContextLength, fmt.Errorf("marshal ollama show request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/show", bytes.NewReader(reqBody))
	if err != nil {
		return defaultContextLength, fmt.Errorf("build ollama show request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return defaultContextLength, fmt.Errorf("ollama show request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return defaultContextLength, fmt.Errorf("ollama show request failed: %s: %s", resp.Status, body)
	}

	var out ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return defaultContextLength, fmt.Errorf("decode ollama show response: %w", err)
	}

	for key, v := range out.ModelInfo {
		if len(key) > len(".context_length") && key[len(key)-len(".context_length"):] == ".context_length" {
			if n, ok := v.(float64); ok && n > 0 {
				return int(n), nil
			}
		}
	}
	return defaultContextLength, fmt.Errorf("context_length not found in model_info for %s", c.model)
}
