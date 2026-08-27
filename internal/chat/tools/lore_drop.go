package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// loreDropMu serializes lore_drop's read-modify-write-then-reingest cycle.
// Chat is stateless and cmd/serve serves requests concurrently, so two
// turns appending to the same file would otherwise both read the old
// bytes and the second write would clobber the first. Writes are rare, so
// one lock for all files is fine.
var loreDropMu sync.Mutex

// loreDrop writes ulmarin lore into the corpus and re-ingests it.
type loreDrop struct{}

func (loreDrop) Name() string      { return "lore_drop" }
func (loreDrop) Writes() bool      { return true }
func (loreDrop) OncePerTurn() bool { return true }
func (loreDrop) Available(d Deps) bool {
	return d.LoreDir != "" && d.Loremaster != nil
}

func (loreDrop) Def() Def {
	var d Def
	d.Type = "function"
	d.Function.Name = "lore_drop"
	d.Function.Description = "Write ulmarin lore into the corpus and immediately re-ingest it so future searches find it. Use ONLY when retrieve_documents confirms the corpus cannot answer a question about the ulmarin. To ADD to an existing document, pass its filename, mode \"append\", and content = ONLY the new Markdown section (the tool keeps the existing text — you do NOT need get_resource first). To create a new topic, pass a new filename like \"06-ulmarin-cuisine.md\" (mode is ignored). Use mode \"replace\" only to rewrite a whole document, passing its COMPLETE new body."
	d.Function.Parameters = map[string]any{
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
	return d
}

// loreDropOutcome is what a successful lore_drop hands back to the model.
type loreDropOutcome struct {
	Filename string `json:"filename"`
	Chunks   int    `json:"chunks"`
	Action   string `json:"action"` // "created" | "appended" | "replaced"
	Note     string `json:"note"`
}

func (loreDrop) Run(ctx context.Context, call Call, deps Deps) Result {
	filename, _ := call.Args["filename"].(string)
	content, _ := call.Args["content"].(string)
	mode, _ := call.Args["mode"].(string)

	base, err := safeLoreName(filename)
	if err != nil {
		return errResult(err)
	}
	if strings.TrimSpace(content) == "" {
		return errResult(fmt.Errorf("missing required argument %q", "content"))
	}

	loreDropMu.Lock()
	defer loreDropMu.Unlock()

	path := filepath.Join(deps.LoreDir, base)
	finalContent, action, err := resolveLoreContent(path, content, mode)
	if err != nil {
		return errResult(err)
	}

	if err := os.MkdirAll(deps.LoreDir, 0o755); err != nil {
		return errResult(err)
	}
	if err := os.WriteFile(path, []byte(finalContent), 0o644); err != nil { //nolint:gosec // corpus docs are world-readable
		return errResult(err)
	}

	n, err := deps.Loremaster.IngestFile(ctx, base, []byte(finalContent))
	if err != nil {
		return errResult(fmt.Errorf("wrote %s but re-ingest failed: %w", base, err))
	}

	return jsonResult(
		fmt.Sprintf("%s %s (now %d chunk(s))", capitalize(action), base, n),
		loreDropOutcome{
			Filename: base,
			Chunks:   n,
			Action:   action,
			Note:     "re-ingested; the corpus is now searchable for this exact content",
		},
	)
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
	// tend to re-send a slab of the current file as a preamble to their
	// new text, so strip a *leading* run of paragraphs that already appear
	// in the document — but stop at the first genuinely-new paragraph and
	// keep everything after it verbatim, so an interior paragraph that
	// merely coincides with existing text (a shared heading, a short
	// sentence) is never dropped.
	fresh := stripEchoedPreamble(string(existing), content)
	if fresh == "" {
		return "", "", errors.New("nothing new to append — that content is already in the document; pass only the genuinely new section, or use mode \"replace\"")
	}
	joined := strings.TrimRight(string(existing), "\n") + "\n\n" + fresh
	return joined, "appended", nil
}

// stripEchoedPreamble drops the leading run of content's blank-line-
// separated paragraphs that already appear (trimmed) anywhere in existing,
// returning the remaining paragraphs rejoined with blank lines. It stops
// at the first paragraph that isn't already present, so only a
// re-transmitted preamble is removed.
func stripEchoedPreamble(existing, content string) string {
	seen := map[string]struct{}{}
	for _, p := range strings.Split(existing, "\n\n") {
		if t := strings.TrimSpace(p); t != "" {
			seen[t] = struct{}{}
		}
	}

	paras := strings.Split(content, "\n\n")
	start := 0
	for start < len(paras) {
		t := strings.TrimSpace(paras[start])
		if t == "" {
			start++
			continue
		}
		if _, dup := seen[t]; !dup {
			break
		}
		start++
	}

	return strings.TrimSpace(strings.Join(paras[start:], "\n\n"))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
