package chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

func TestAnswerSources(t *testing.T) {
	retrieved := []store.SearchResult{
		{Source: "a.md", ChunkIndex: 2},
		{Source: "a.md", ChunkIndex: 5},
		{Source: "b.md", ChunkIndex: 0},
	}

	t.Run("keeps only cited files when the answer cites", func(t *testing.T) {
		got := answerSources("The drift-circles migrate [a.md]. Unrelated aside.", retrieved)
		require.Len(t, got, 1)
		assert.Equal(t, "a.md", got[0].File)
		assert.Equal(t, []int{2, 5}, got[0].ChunkIndices)
	})

	t.Run("falls back to every retrieved file when the answer cites nothing", func(t *testing.T) {
		got := answerSources("A plain answer with no markers.", retrieved)
		require.Len(t, got, 2)
		assert.Equal(t, "a.md", got[0].File)
		assert.Equal(t, "b.md", got[1].File)
	})

	t.Run("ignores citations to files that were never retrieved", func(t *testing.T) {
		got := answerSources("As noted in [c.md] and [a.md].", retrieved)
		require.Len(t, got, 1)
		assert.Equal(t, "a.md", got[0].File)
	})

	t.Run("nothing retrieved yields no sources", func(t *testing.T) {
		assert.Nil(t, answerSources("cites [a.md]", nil))
	})
}

func TestRun_EmitsSources(t *testing.T) {
	srv := newScriptedServer(
		toolCallLines("retrieve_documents", map[string]any{"query": "diet"}),
		okLines("Ulmarin eat glowfronds [03-diet.md]."),
		okLines("OK"),
	)
	defer srv.Close()

	retriever := &fakeRetriever{results: []store.SearchResult{
		{Source: "03-diet.md", ChunkIndex: 1, Content: "glowfronds"},
		{Source: "07-myth.md", ChunkIndex: 4, Content: "unused"},
	}}
	c := &Chatter{Client: NewOllamaChat(srv.URL, "test-model"), Retriever: retriever, DefaultTopK: 3}
	events := collectEvents(t, c, []Message{{Role: "user", Content: "what do they eat?"}})

	var sources []SourceRef
	for _, e := range events {
		if e.Type == EventSources {
			sources = e.Sources
		}
	}
	require.Len(t, sources, 1, "only the cited file should be surfaced")
	assert.Equal(t, "03-diet.md", sources[0].File)
	assert.Equal(t, []int{1}, sources[0].ChunkIndices)
}
