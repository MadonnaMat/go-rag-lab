// Package queries holds the SQL that internal/store runs, one statement per
// .sql file, embedded into these exported strings.
//
// Why files and a sub-package rather than raw-string consts in store.go:
// the hybrid search statement is long enough that inlining it buries the
// surrounding Go control flow, and //go:embed paths can't escape their own
// directory — so the files (and therefore the var block that embeds them)
// have to live together in their own directory, which in Go means their own
// package. Trivial one-liners (`SELECT count(*) FROM chunks`, the orphan
// DELETE) stay inline at their call sites; a file each would be noise.
package queries

import _ "embed"

var (
	//go:embed search_vector.sql
	SearchVector string
	//go:embed search_keyword.sql
	SearchKeyword string
	//go:embed search_auto.sql
	SearchAuto string

	//go:embed upsert_document.sql
	UpsertDocument string
	//go:embed insert_chunk.sql
	InsertChunk string
	//go:embed list_documents.sql
	ListDocuments string
	//go:embed upsert_corpus_summary.sql
	UpsertCorpusSummary string
	//go:embed upsert_ingest_dir_hash.sql
	UpsertIngestDirHash string
)
