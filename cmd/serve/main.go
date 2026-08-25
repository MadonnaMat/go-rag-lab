// Command serve runs an HTTP API that embeds a user's query and returns the
// nearest chunks from Postgres+pgvector. It's a thin wrapper: all the real
// logic lives in internal/retrieve, internal/api, internal/embedding, and
// internal/store — this file just parses flags and wires them together.
//
// Requires the database schema to already exist — run `make migrate` (or
// `docker compose run --rm migrate`) first, same precondition cmd/ingest
// has.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/MadonnaMat/go-rag-lab/internal/api"
	"github.com/MadonnaMat/go-rag-lab/internal/config"
	"github.com/MadonnaMat/go-rag-lab/internal/embedding"
	"github.com/MadonnaMat/go-rag-lab/internal/retrieve"
	"github.com/MadonnaMat/go-rag-lab/internal/store"
)

func main() {
	addr := flag.String("addr", "", "address to listen on, e.g. :8080 (overrides SERVER_ADDR)")
	flag.Parse()

	if err := run(*addr); err != nil {
		log.Fatal(err)
	}
}

func run(addrFlag string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	addr := cfg.ServerAddr
	if addrFlag != "" {
		addr = addrFlag
	}

	ctx := context.Background()

	provider, err := embedding.New(cfg.EmbeddingProvider, cfg.OllamaURL, cfg.OllamaEmbedModel)
	if err != nil {
		return fmt.Errorf("build embedding provider: %w", err)
	}

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	retriever := &retrieve.Retriever{Store: s, Provider: provider}
	handler := api.NewRouter(&api.Handler{Retriever: retriever, DefaultTopK: cfg.TopK})

	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, handler)
}
