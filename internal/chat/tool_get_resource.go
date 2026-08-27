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

// getResourceToolName is the function name exposed to the model.
const getResourceToolName = "get_resource"

// getResourceToolDef describes the whole-document read tool.
func getResourceToolDef() toolDef {
	var t toolDef
	t.Type = "function"
	t.Function.Name = getResourceToolName
	t.Function.Description = "Return the full Markdown text of one ingested document, named exactly as list_resources reports it (e.g. \"05-ulmarin-language.md\"). Use this when the user wants a whole document rather than search snippets, or to read a document before extending it with lore_drop."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The document filename, exactly as list_resources reports it.",
			},
		},
		"required": []string{"name"},
	}
	return t
}

// safeLoreName validates a model-supplied filename: it must be a bare
// ".md" base name with no directory component, so a tool can only touch
// files directly inside LoreDir.
func safeLoreName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("missing required argument \"name\"")
	}
	if name != filepath.Base(name) || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("invalid resource name %q: must be a bare filename", name)
	}
	if filepath.Ext(name) != ".md" {
		return "", fmt.Errorf("invalid resource name %q: must end in .md", name)
	}
	return name, nil
}

// runGetResourceTool reads one .md file from c.LoreDir.
func (c *Chatter) runGetResourceTool(_ context.Context, tc toolCall, emit func(Event) error) (chatMessage, error) {
	name, _ := tc.Function.Arguments["name"].(string)
	if err := emit(Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments}); err != nil {
		return chatMessage{}, err
	}

	if c.LoreDir == "" {
		return toolErrorMessage(tc, emit, fmt.Errorf("reading documents is not available"))
	}

	base, err := safeLoreName(name)
	if err != nil {
		return toolErrorMessage(tc, emit, err)
	}

	content, err := os.ReadFile(filepath.Join(c.LoreDir, base))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return toolErrorMessage(tc, emit, fmt.Errorf("no such resource %q", base))
		}
		return toolErrorMessage(tc, emit, err)
	}

	payload, _ := json.Marshal(struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}{Name: base, Content: string(content)})
	summary := fmt.Sprintf("Read %s (%d chars)", base, len(content))
	return toolResultMessage(tc, emit, summary, string(payload))
}
