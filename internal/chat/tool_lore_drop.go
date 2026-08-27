package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// loreDropToolName is the function name exposed to the model.
const loreDropToolName = "lore_drop"

// loreDropToolDef describes the write-new-lore-and-re-ingest tool.
func loreDropToolDef() toolDef {
	var t toolDef
	t.Type = "function"
	t.Function.Name = loreDropToolName
	t.Function.Description = "Write ulmarin lore into the corpus and immediately re-ingest it so future searches find it. Use ONLY when retrieve_documents confirms the corpus cannot answer a question about the ulmarin. To ADD to an existing document, pass its filename, mode \"append\", and content = ONLY the new Markdown section (the tool keeps the existing text — you do NOT need get_resource first). To create a new topic, pass a new filename like \"06-ulmarin-cuisine.md\" (mode is ignored). Use mode \"replace\" only to rewrite a whole document, passing its COMPLETE new body."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filename": map[string]any{
				"type":        "string",
				"description": "Target .md filename, bare name, no path (e.g. \"06-ulmarin-cuisine.md\").",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "For mode \"append\": only the new Markdown section to add. For a new file or mode \"replace\": the complete Markdown body.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"append", "replace"},
				"description": "\"append\" (default) adds content to the end of an existing file; \"replace\" overwrites it. Ignored when the file does not exist yet.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "One short line on what was added and why (shown to the user).",
			},
		},
		"required": []string{"filename", "content"},
	}
	return t
}

// loreDropAlreadySucceeded reports whether an earlier lore_drop in this
// same Run wrote successfully (its tool result carries an "action" field;
// error results don't).
func loreDropAlreadySucceeded(msgs []chatMessage) bool {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolName == loreDropToolName && strings.Contains(m.Content, `"action":`) {
			return true
		}
	}
	return false
}

// loreDropOutcome is what runLoreDropTool hands back to the model.
type loreDropOutcome struct {
	Filename string `json:"filename"`
	Chunks   int    `json:"chunks"`
	Action   string `json:"action"` // "created" | "appended" | "replaced"
	Note     string `json:"note"`
}

// runLoreDropTool writes content to c.LoreDir/<filename> (creating,
// appending, or replacing per mode) and re-ingests just that file via
// c.Loremaster.
func (c *Chatter) runLoreDropTool(ctx context.Context, prior []chatMessage, tc toolCall, emit func(Event) error) (chatMessage, error) {
	filename, _ := tc.Function.Arguments["filename"].(string)
	content, _ := tc.Function.Arguments["content"].(string)
	mode, _ := tc.Function.Arguments["mode"].(string)

	if err := emit(Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}); err != nil {
		return chatMessage{}, err
	}

	if c.LoreDir == "" || c.Loremaster == nil {
		return toolErrorMessage(tc, emit, fmt.Errorf("adding lore is not available"))
	}

	// Weak models keep re-calling lore_drop with lightly-reworded content
	// after it already succeeded. One write per turn: send them back to
	// answering the user.
	if loreDropAlreadySucceeded(prior) {
		return toolErrorMessage(tc, emit, errors.New("lore was already saved earlier in this turn — do not call lore_drop again; answer the user now using what you wrote"))
	}

	base, err := safeLoreName(filename)
	if err != nil {
		return toolErrorMessage(tc, emit, err)
	}
	if strings.TrimSpace(content) == "" {
		return toolErrorMessage(tc, emit, fmt.Errorf("missing required argument \"content\""))
	}

	path := filepath.Join(c.LoreDir, base)
	finalContent, action, err := resolveLoreContent(path, content, mode)
	if err != nil {
		return toolErrorMessage(tc, emit, err)
	}

	if err := os.MkdirAll(c.LoreDir, 0o755); err != nil {
		return toolErrorMessage(tc, emit, err)
	}
	if err := os.WriteFile(path, []byte(finalContent), 0o644); err != nil {
		return toolErrorMessage(tc, emit, err)
	}

	n, err := c.Loremaster.IngestFile(ctx, base, []byte(finalContent))
	if err != nil {
		return toolErrorMessage(tc, emit, fmt.Errorf("wrote %s but re-ingest failed: %w", base, err))
	}

	payload, _ := json.Marshal(loreDropOutcome{
		Filename: base,
		Chunks:   n,
		Action:   action,
		Note:     "re-ingested; the corpus is now searchable for this exact content",
	})
	summary := fmt.Sprintf("%s %s (now %d chunk(s))", capitalize(action), base, n)
	return toolResultMessage(tc, emit, summary, string(payload))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// resolveLoreContent computes the bytes to write and a label for what
// happened, given the existing file (if any), the model's content, and the
// requested mode ("append" default, or "replace").
func resolveLoreContent(path, content, mode string) (finalContent, action string, err error) {
	existing, readErr := os.ReadFile(path) //nolint:gosec // path is safeLoreName-validated, inside LoreDir
	switch {
	case errors.Is(readErr, fs.ErrNotExist):
		return content, "created", nil
	case readErr != nil:
		return "", "", readErr
	}

	if mode == "replace" {
		return content, "replaced", nil
	}

	// Default: append, keeping the existing document intact. Weak models
	// tend to re-send big slabs of the current file alongside their new
	// text, so drop any paragraph that already appears verbatim rather
	// than duplicating it.
	fresh := newParagraphs(string(existing), content)
	if fresh == "" {
		return "", "", errors.New("nothing new to append — that content is already in the document; pass only the genuinely new section, or use mode \"replace\"")
	}
	joined := strings.TrimRight(string(existing), "\n") + "\n\n" + fresh
	return joined, "appended", nil
}

// newParagraphs returns the blank-line-separated paragraphs of content
// that don't already appear (trimmed) in existing, rejoined with blank
// lines.
func newParagraphs(existing, content string) string {
	seen := map[string]struct{}{}
	for _, p := range strings.Split(existing, "\n\n") {
		if t := strings.TrimSpace(p); t != "" {
			seen[t] = struct{}{}
		}
	}

	var kept []string
	for _, p := range strings.Split(content, "\n\n") {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		kept = append(kept, t)
	}
	return strings.Join(kept, "\n\n")
}
