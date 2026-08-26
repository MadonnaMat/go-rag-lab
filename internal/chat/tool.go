package chat

// retrieveToolName is the function name exposed to the model.
const retrieveToolName = "retrieve_documents"

// retrieveToolDef describes the retrieval tool the model can call.
func retrieveToolDef() toolDef {
	var t toolDef
	t.Type = "function"
	t.Function.Name = retrieveToolName
	t.Function.Description = "Search the ingested document corpus for chunks relevant to a query. Use this whenever answering requires specific facts from the documents rather than general knowledge."
	t.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to embed and match against document chunks.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Number of chunks to return (optional).",
			},
		},
		"required": []string{"query"},
	}
	return t
}

// toolResultChunk is what a retrieve_documents call hands back to the
// model, mirroring store.SearchResult.
type toolResultChunk struct {
	Source   string  `json:"source"`
	Content  string  `json:"content"`
	Distance float64 `json:"distance"`
}
