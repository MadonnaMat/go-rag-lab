package tools

import (
	"context"
	"fmt"
)

// listResources enumerates every ingested document.
type listResources struct{}

func (listResources) Name() string          { return "list_resources" }
func (listResources) Available(d Deps) bool { return d.Docs != nil }
func (listResources) Writes() bool          { return false }
func (listResources) OncePerTurn() bool     { return false }

func (listResources) Def() Def {
	var d Def
	d.Type = "function"
	d.Function.Name = "list_resources"
	d.Function.Description = "List every document that has been ingested into the corpus, with its chunk count. Use this to answer \"what documents/topics do you have?\" and to see what exists before reading a whole document or adding new lore."
	d.Function.Parameters = map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	return d
}

// listResource is one entry in the list_resources result.
type listResource struct {
	Name   string `json:"name"`
	Chunks int    `json:"chunks"`
}

func (listResources) Run(ctx context.Context, _ Call, deps Deps) Result {
	docs, err := deps.Docs.ListDocuments(ctx)
	if err != nil {
		return errResult(err)
	}
	out := make([]listResource, len(docs))
	for i, doc := range docs {
		out[i] = listResource{Name: doc.Path, Chunks: doc.Chunks}
	}
	return jsonResult(fmt.Sprintf("%d document(s) in the corpus", len(out)), out)
}
