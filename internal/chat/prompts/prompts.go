// Package prompts holds the chat orchestration's prompt text as embedded
// Markdown files rather than Go string literals, so they can be edited
// (and reviewed in a diff) as plain prose.
package prompts

import (
	_ "embed"
	"strings"
)

//go:embed system.md
var systemRaw string

// System is the default system prompt guiding the model on when to use
// the retrieval tool.
var System = strings.TrimSpace(systemRaw)

//go:embed verify.md
var verifyRaw string

// Verify is the post-answer self-check prompt template — it has one %s
// placeholder for the draft answer text.
var Verify = strings.TrimSpace(verifyRaw)

//go:embed compact.md
var compactRaw string

// Compact is the prompt used to summarize older conversation turns during
// auto-compaction.
var Compact = strings.TrimSpace(compactRaw)

//go:embed corpus_summary.md
var corpusSummaryRaw string

// CorpusSummary is the prompt cmd/ingest uses to generate a one-time
// description of the ingested corpus — it has one %s placeholder for
// sampled document excerpts.
var CorpusSummary = strings.TrimSpace(corpusSummaryRaw)
