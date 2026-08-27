package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// loreDropToolName is the function name exposed to the model.
const loreDropToolName = "lore_drop"

// loreDropToolDef describes the write-new-lore-and-re-ingest tool.
func loreDropToolDef() toolDef {
	var t toolDef
	t.Type = "function"
	t.Function.Name = loreDropToolName
	t.Function.Description = "Write a new or updated Markdown document about the ulmarin race into the corpus and immediately re-ingest it, so future searches can find it. Use this ONLY when retrieve_documents (and, if useful, get_resource) confirm the corpus genuinely cannot answer a question about the ulmarin. To extend an existing document, first get_resource it, then pass the COMPLETE updated file body here under its existing filename. To add a new topic, pass a new filename like \"06-ulmarin-cuisine.md\"."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filename": map[string]any{
				"type":        "string",
				"description": "Target .md filename (bare name, no path). An existing name overwrites that document; a new name creates one.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The COMPLETE Markdown body of the file, not a fragment. When updating an existing document, include its prior content plus your additions.",
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

// runLoreDropTool writes content to c.LoreDir/<filename> and re-ingests
// just that file via c.Loremaster.
func (c *Chatter) runLoreDropTool(ctx context.Context, tc toolCall, emit func(Event) error) (chatMessage, error) {
	filename, _ := tc.Function.Arguments["filename"].(string)
	content, _ := tc.Function.Arguments["content"].(string)

	if err := emit(Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}); err != nil {
		return chatMessage{}, err
	}

	if c.LoreDir == "" || c.Loremaster == nil {
		return toolErrorMessage(tc, emit, fmt.Errorf("adding lore is not available"))
	}

	base, err := safeLoreName(filename)
	if err != nil {
		return toolErrorMessage(tc, emit, err)
	}
	if content == "" {
		return toolErrorMessage(tc, emit, fmt.Errorf("missing required argument \"content\""))
	}

	path := filepath.Join(c.LoreDir, base)
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)

	if err := os.MkdirAll(c.LoreDir, 0o755); err != nil {
		return toolErrorMessage(tc, emit, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return toolErrorMessage(tc, emit, err)
	}

	n, err := c.Loremaster.IngestFile(ctx, base, []byte(content))
	if err != nil {
		return toolErrorMessage(tc, emit, fmt.Errorf("wrote %s but re-ingest failed: %w", base, err))
	}

	payload, _ := json.Marshal(struct {
		Filename string `json:"filename"`
		Chunks   int    `json:"chunks"`
		Created  bool   `json:"created"`
		Note     string `json:"note"`
	}{
		Filename: base,
		Chunks:   n,
		Created:  created,
		Note:     "re-ingested; the corpus is now searchable for this content",
	})

	verb := "Updated"
	if created {
		verb = "Created"
	}
	summary := fmt.Sprintf("%s & re-ingested %s (%d chunk(s))", verb, base, n)
	return toolResultMessage(tc, emit, summary, string(payload))
}
