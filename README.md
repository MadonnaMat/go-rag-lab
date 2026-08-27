# go-rag-lab

[![CI](https://github.com/MadonnaMat/go-rag-lab/actions/workflows/ci.yml/badge.svg)](https://github.com/MadonnaMat/go-rag-lab/actions/workflows/ci.yml)

Week 3 Technical Hands-on Lab (job-hunt schedule): AI Engineering & RAG
Architecture, built in Go instead of Ruby/Node — chosen to diversify beyond
the Rails/React stack used in Weeks 1-2 of `prototype_app`.

Go module scaffold, PostgreSQL + pgvector, document ingestion + embedding,
a `GET /query` retrieval API, and a tool-calling RAG chat endpoint
(`POST /chat`, streamed over SSE) with a small HTMX + Alpine.js + Tailwind
frontend — all with tests and CI. See `CLAUDE.md` for the full picture
(commands, architecture, conventions).

## Architecture

```
cmd/{ingest,serve,migrate}   thin CLIs: flags + wiring only
internal/
  config/                    env-var loading with defaults
  chunk/                     pure text chunking (fixed-size, overlapping, rune-safe)
  embedding/                 pluggable Provider interface; Ollama is today's only impl
  store/                     Postgres+pgvector persistence (pgx + pgvector-go)
  ingest/                    orchestration: walk dir → chunk → embed → store (+ a
                              one-time corpus-summary generation via internal/chat)
  retrieve/                  orchestration: embed a query → nearest chunks
  chat/                      orchestration: Ollama chat client + tool-calling loop,
                              auto-compaction, answer verification
  api/                       thin chi HTTP layer over retrieve/chat (GET /query,
                              POST /chat, the web/ frontend, /healthz, /swagger)
web/                         HTMX + Alpine.js + Tailwind chat page, go:embed'd
                              into cmd/serve's binary (see web/embed.go)
```

Each package only depends on the ones below it in that list — `chunk` has
zero dependencies, `embedding` and `store` don't know about each other, and
`ingest`/`retrieve`/`chat` are the orchestration layers that wire lower
packages together without those lower packages knowing about each other.
Adding a second embedding provider (e.g. OpenAI) means one new file in
`internal/embedding` plus one new `case` in its factory function — nothing
else changes.

## Prerequisites

- Go 1.26+ (`sudo apt-get install golang-go` on this machine)
- Docker — via Docker Desktop's WSL integration on this machine, not a
  native `docker.io` install (see `CLAUDE.md` for details)
- Ollama, host-installed (not containerized, so it can use the GPU
  directly) — `scripts/ollama-dev` installs and manages it for you

## Setup

```sh
cp .env.example .env
go mod download
make up                       # starts Postgres+pgvector via docker-compose
make migrate                  # applies the schema
scripts/ollama-dev --daemon   # installs Ollama if needed, pulls the embedding + chat models
```

Or, for the one-command version of all of the above plus ingesting
`sample_docs/` and starting the server: `make dev-up` (see `CLAUDE.md`).
Then visit `http://localhost:8080/` for the chat page, or
`http://localhost:8080/swagger/index.html` for the API docs.

## Running ingestion

Natively (fastest inner loop):

```sh
make ingest
```

Containerized (proves the full Docker path, including reaching host
Ollama via `host.docker.internal`):

```sh
docker compose up --build app
```

Either way, re-running is safe — ingesting the same file again replaces its
chunks rather than duplicating them (see `internal/store.Store.ReplaceChunks`).

Verify rows landed:

```sh
docker exec -it $(docker compose ps -q db) psql -U rag -d rag -c "SELECT count(*) FROM documents;"
docker exec -it $(docker compose ps -q db) psql -U rag -d rag -c "SELECT count(*) FROM chunks;"
```

The sample corpus in `sample_docs/` is a small fictional-worldbuilding set
(the *Ulmarin*, an ocean-dwelling alien species) rather than real
documentation — plenty of distinct semantic content to chunk/embed without
depending on anything external.

## Testing

```sh
make test-unit   # no infra needed — pure logic + httptest-mocked Ollama
make test        # also runs DB integration tests against `make up`'s Postgres
make test-web    # headless-Chrome tests for the chat frontend (needs Chrome/Chromium)
```

DB-dependent tests run against a separate `rag_test` database (created
automatically by `docker/initdb/01-create-test-db.sql`), not the same
database `make ingest` populates with real data — see `TEST_DATABASE_URL`
in `.env.example`.

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs `make build`, `make lint`,
`make test`, `make test-web`, and `make ci-verify` on every push/PR, using
the exact same Makefile targets described above (backed by a
`pgvector/pgvector:pg17` service container; `ubuntu-latest` ships Chrome
preinstalled for `test-web`). `make test`'s embedding/chat-provider tests
mock Ollama's HTTP API via `httptest`, so they need nothing live — but the
separate `make ci-verify` step does bring up a real, pre-baked CI-only
Ollama image (see `CLAUDE.md`) and runs actual embedding and chat/tool-call
requests against it end-to-end.
