package chat

import (
	"regexp"
	"sort"
	"strings"

	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

// citationRe matches an inline citation marker like "[03-ulmarin-diet.md]"
// the model is asked (in prompts/system.md) to drop after facts it used.
var citationRe = regexp.MustCompile(`\[([^\[\]/\\]+\.md)\]`)

// answerSources works out which ingested documents the final answer drew on.
//
// Primary signal: the [name.md] markers the model left in its answer, kept
// only if they match a document actually retrieved this turn. Fallback: if
// the model cited nothing, every distinct document retrieved this turn — a
// coarser "here's what was on the table" list rather than nothing.
//
// Either way the result is ordered by first retrieval and each entry
// carries the sorted, de-duplicated chunk indices seen from that file.
func answerSources(answer string, retrieved []store.SearchResult) []SourceRef {
	if len(retrieved) == 0 {
		return nil
	}

	// file -> set of chunk indices, plus first-seen order.
	idxByFile := map[string]map[int]struct{}{}
	var order []string
	for _, r := range retrieved {
		if _, seen := idxByFile[r.Source]; !seen {
			idxByFile[r.Source] = map[int]struct{}{}
			order = append(order, r.Source)
		}
		idxByFile[r.Source][r.ChunkIndex] = struct{}{}
	}

	cited := map[string]struct{}{}
	for _, m := range citationRe.FindAllStringSubmatch(answer, -1) {
		name := strings.TrimSpace(m[1])
		if _, ok := idxByFile[name]; ok {
			cited[name] = struct{}{}
		}
	}

	keep := func(string) bool { return true }
	if len(cited) > 0 {
		keep = func(f string) bool { _, ok := cited[f]; return ok }
	}

	var out []SourceRef
	for _, f := range order {
		if !keep(f) {
			continue
		}
		idxSet := idxByFile[f]
		indices := make([]int, 0, len(idxSet))
		for i := range idxSet {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		out = append(out, SourceRef{File: f, ChunkIndices: indices})
	}
	return out
}
