package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/MadonnaMat/go-rag-lab/internal/lore"
)

// getResource returns the full Markdown text of one ingested document.
type getResource struct{}

func (getResource) Name() string          { return "get_resource" }
func (getResource) Available(d Deps) bool { return d.LoreDir != "" }
func (getResource) Writes() bool          { return false }
func (getResource) OncePerTurn() bool     { return false }

func (getResource) Def() Def {
	var d Def
	d.Type = "function"
	d.Function.Name = "get_resource"
	d.Function.Description = "Return the full Markdown text of one ingested document, named exactly as list_resources reports it (e.g. \"05-ulmarin-language.md\"). Use this when the user wants a whole document rather than search snippets."
	d.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The document filename, exactly as list_resources reports it.",
			},
		},
		"required": []string{"name"},
	}
	return d
}

func (getResource) Run(_ context.Context, call Call, deps Deps) Result {
	name, _ := call.Args["name"].(string)
	base, err := lore.SafeName(name)
	if err != nil {
		return errResult(err)
	}

	content, err := os.ReadFile(filepath.Join(deps.LoreDir, base)) //nolint:gosec // base is safeLoreName-validated
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errResult(fmt.Errorf("no such resource %q", base))
		}
		return errResult(err)
	}

	return jsonResult(fmt.Sprintf("Read %s (%d chars)", base, len(content)), struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}{Name: base, Content: string(content)})
}
