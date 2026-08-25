# go-rag-lab

Week 3 of a self-directed job-hunt technical lab (AI Engineering & RAG
Architecture, built in Go). See `~/.claude/skills/job-hunt-today/progress-log.md`
for the full week's schedule — this repo has ingestion (`internal/ingest` /
`cmd/ingest`) and a query/retrieval REST endpoint (`internal/retrieve` /
`internal/api` / `cmd/serve`) so far; a chat endpoint and a client are later
days and don't exist yet, don't assume they do.

## Commands

- `make build` / `make vet` / `make fmt` / `make lint` (= fmt-check + vet)
- `make test-unit` — pure logic + httptest-mocked Ollama, no infra needed
- `make test` — also runs DB integration tests, against `rag_test` (not
  dev's `rag` database — see below). Requires `make up` running.
- `make up` / `make down` — docker-compose's `db` service
- `make migrate` / `make migrate-down` — apply/roll back
  `internal/store/migrations/*.sql` natively (see "Database migrations"
  below). Run once against a fresh database before `make ingest` or
  `cmd/serve` will work.
- `make ingest` — runs the ingestion CLI natively against `sample_docs/`
- `make ci-verify` — CI only: brings up `db` + a pre-baked CI-only Ollama
  (see below), then builds and runs the `app` service against them for
  real. Not run by `make up` / local dev.
- `docker compose run --rm migrate` — same as `make migrate`, but
  containerized (one-shot, not `up`)
- `docker compose up --build app` — runs ingestion in the containerized
  path instead of natively, against your host-installed Ollama
- `docker compose up --build serve` — runs the query API in the
  containerized path instead of natively (`go run ./cmd/serve`), against
  your host-installed Ollama
- `make swagger` — regenerates `docs/` (gitignored, not committed — see
  "API docs" below), the OpenAPI spec, from current source annotations;
  also a prerequisite of `build`/`vet`/`test-unit`/`test`. With `cmd/serve`
  running, live Swagger UI is at `http://<addr>/swagger/index.html`.
- `scripts/ollama-dev --daemon` — installs Ollama if needed, ensures the
  systemd service is running, pulls the embedding model
- `make dev-up` (`scripts/dev-up`) — one-command local verification loop:
  starts Ollama + `db` (only if not already running), migrates, ingests
  `sample_docs/`, then runs `cmd/serve` in the foreground so you can go
  check the browser. Ctrl-C stops `cmd/serve` and, via a trap, anything
  else this specific invocation started — it leaves alone whatever was
  already running before it was called.

The `Dockerfile` is multi-stage: one shared build stage compiles all three
binaries (`ingest`/`serve`/`migrate`), and each gets its own thin final
stage, selected via `docker-compose.yml`'s `build.target` — one file
instead of one per binary that could drift apart.

CI (`.github/workflows/ci.yml`) calls these same `make` targets — never a
hand-copied raw command — specifically so CI can't silently drift from what
running them locally actually does. `db` in CI comes from our own
`docker-compose.yml` too (not a separate GH Actions `services:` block), so
there's one definition of the Postgres+pgvector setup, not two that could
drift apart.

## Architecture

`internal/chunk` (pure, zero deps) → `internal/embedding` (`Provider`
interface, `Ollama` is the only implementation today, selected via
`EMBEDDING_PROVIDER`) and `internal/store` (pgx + pgvector-go persistence,
neither of which know about each other) → `internal/ingest` (the only
package that wires all three together) → `cmd/ingest/main.go` (thin: flags
+ wiring only). Adding a second embedding provider is one new file in
`internal/embedding` plus one new `case` in its `New()` factory — nothing
else changes.

The query side mirrors this exactly: `internal/embedding` + `internal/store`
→ `internal/retrieve` (embeds a query with the same `Provider` used at
ingestion, then asks the store for the nearest chunks) → `internal/api` (a
thin `chi` HTTP layer: `GET /query`, `GET /healthz`, `GET /swagger/*`) →
`cmd/serve/main.go` (thin: flags + wiring only). `/query` is `GET` with
`query`/`top_k` as URL query params, not `POST` with a JSON body — a search
here is a safe, idempotent read (no state changes, no side effects), which
is exactly what `GET` semantics are for; it also means it's directly
testable by pasting a URL into a browser. Don't reflexively default a new
JSON-shaped endpoint to `POST` — check whether it's actually a read first.
`ingest.Store` and
`retrieve.Store` are each a small interface (not `*store.Store` directly)
so tests substitute a fake with no real Postgres involved — Go interfaces
are satisfied structurally, so `*store.Store` never had to declare it
implements anything. Same reasoning for `api.Retriever` not depending on
`*retrieve.Retriever` directly.

`internal/store`'s schema lives in versioned migrations
(`internal/store/migrations/`), not a single embedded file — see "Database
migrations" below.

## Database migrations

Schema changes are a new numbered migration pair in
`internal/store/migrations/` (`NNNNNN_description.up.sql` /
`.down.sql`), applied via `golang-migrate` — never an edit to an old
migration. `internal/store/migrate.go` embeds them into the binary and
exposes `store.MigrateUp` / `store.MigrateDown`, wrapped by the thin
`cmd/migrate` binary.

Applying migrations is a deliberate, explicit step — `make migrate` /
`make migrate-down` (native) or `docker compose run --rm migrate`
(containerized) — not something `cmd/ingest` or `cmd/serve` does for you on
startup. Run it once against a fresh database before either will work.
`make test` runs it automatically against `TEST_DATABASE_URL` first, so it
stays a single command; `scripts/ci-verify` does the same against the dev
`rag` database via the containerized `migrate` service before the
containerized ingest run.

## API docs (Swagger/OpenAPI)

`docs/` (`docs.go` + `swagger.json` + `swagger.yaml`) is generated by
`swaggo/swag` from `@`-annotation doc-comments — the general API info block
above `func main()` in `cmd/serve/main.go`, and per-endpoint annotations
(`@Summary`, `@Param`, `@Success`, `@Router`, etc.) above each handler in
`internal/api/api.go`. **Never hand-edit `docs/`, and it's gitignored, not
committed** — it's pure build output (unlike `internal/store/migrations/`,
which is hand-authored source and does belong in git), so committing it
would just be redundant, diff-noisy generated-file churn on every
annotation tweak. `build`, `vet`, `test-unit`, and `test` all depend on a
`swagger` Make target that regenerates it first (`go tool swag init -g
cmd/serve/main.go -o docs`; the `Dockerfile`'s build stage does the same),
so any of those commands works from a fresh clone — the one gap is running
a bare `go build`/`go vet`/`go test` directly, bypassing `make` entirely,
before any `make` target has run once; that needs `make swagger` (or any
of the four targets above) run first, same "explicit one-time step"
precondition `make migrate` already has for `make ingest`/`cmd/serve`.
`swag` itself is a `tool` dependency in `go.mod` (Go 1.24+ tool tracking,
run via `go tool swag` — no separate global install). Request/response
types in `internal/api` (`QueryRequest`, `QueryResponse`, `QueryResult`)
are exported specifically so `swag`'s reflection-based schema generation
can introspect them — don't make them unexported again without checking
`make swagger` still works.

## Conventions

- Prefer common industry tools or stdlib over inventing bespoke
  abstractions — e.g. `testify` for assertions, `chi` for HTTP routing,
  `golang-migrate` for schema migrations, `swaggo/swag` for API docs. Still
  no CLI framework, no `godotenv`. Table-driven tests (`t.Run` subtests) are
  the default test shape.
- Three tiers of test, from fastest/least-real to slowest/most-real: unit
  (fakes everywhere, `make test-unit`, no infra) → DB integration
  (`internal/store`'s and `internal/api`'s `DATABASE_URL`-gated tests: real
  Postgres, fake embedding `Provider`, `make test`) → full real-stack smoke
  test (`scripts/ci-verify`: real Postgres *and* real Ollama, containerized).
  A change to `SearchChunks`/`Retriever`/the HTTP layer should usually get a
  test at the DB-integration tier; only the "does this work against a real
  embedding model" question belongs in `ci-verify`.
- DB-dependent tests read `DATABASE_URL` and `t.Skip()` if it's unset —
  `go test ./...` always works with zero infra running.
- Document identity in the store is the **filename alone**, not the full
  disk path — see the comment on `ingest.ingestFile`. This was a real bug
  caught by comparing native vs. containerized ingestion runs: the
  Dockerfile's `-dir=/app/sample_docs` vs. native `-dir=sample_docs`
  produced two different path strings for the same file, so `ON CONFLICT
  (path)` never matched and rows duplicated instead of replacing. Don't
  reintroduce full-path identity without re-checking this.
- Tests never touch the same database `make ingest` populates — see
  `TEST_DATABASE_URL` / `docker/initdb/01-create-test-db.sql`.

## The CI-only Ollama image (`docker/ollama-ci/`)

`ghcr.io/madonnamat/go-rag-lab-ollama-ci` is a custom image with
`nomic-embed-text` pre-baked in at build time (not pulled at container
start), so CI never waits on a model download. It's public, linked to this
repo, and used only by `make ci-verify` / `docker-compose.yml`'s `ollama`
service — local dev keeps using host-installed Ollama for GPU access (see
below), this image is CPU-only and CI-only.

It's built `FROM ollama/ollama:latest` but only as a multi-stage COPY
source — the official image bundles CUDA/ROCm/Vulkan GPU backends (~4GB
combined) that GitHub's GPU-less runners can never use, so the Dockerfile
cherry-picks just the CPU backend files into a fresh minimal base,
dropping the GPU directories entirely (320MB vs. 3.4GB unfiltered — see
the Dockerfile's comments for exactly which files and why this works
without rebuilding Ollama from source).

Rebuild and push it if the embedding model or Ollama version ever needs
bumping:

```sh
docker build -t ghcr.io/madonnamat/go-rag-lab-ollama-ci:latest docker/ollama-ci
gh auth token | docker login ghcr.io -u MadonnaMat --password-stdin
docker push ghcr.io/madonnamat/go-rag-lab-ollama-ci:latest
```

(`gh auth token` needs the `write:packages` scope — `gh auth status` shows
current scopes; `gh auth refresh -s write:packages` adds it, but that's an
interactive browser flow the user needs to run themselves, not something
to invoke blind.)

## Local environment notes (this machine)

- **Docker** comes from Docker Desktop on Windows via WSL integration —
  there is no native `docker.io` install in this WSL2 distro. `docker` /
  `docker compose` just work.
- **Ollama** is host-installed (systemd service `ollama`, managed via
  `scripts/ollama-dev`), not containerized — deliberate, so it can use the
  host's NVIDIA GPU directly. Docker services reach it via
  `host.docker.internal`.
- **`/etc/sudoers.d/ollama`** grants passwordless sudo for starting and
  stopping the `ollama` systemd service — Claude Code can run
  `scripts/ollama-dev --daemon` and `scripts/ollama-dev --daemon stop`
  directly without needing a password (confirmed working both ways; don't
  assume raw `sudo systemctl ...` commands outside this script are covered
  by the same grant). No other sudo (apt installs, Docker, etc.) is
  passwordless here — those need the user to run the command themselves in
  their own terminal.
- Commands needing interactive human input — a sudo password prompt, `gh
  auth refresh`'s browser device-flow — will hang if run blind. Ask the
  user to run them directly instead.
- Stop any server processes started while carrying out a plan once its
  verification is done — `make down` for `db`, killing a `go run
  ./cmd/serve` process, `docker compose down`/`stop` for containerized
  services — rather than leaving them running after the turn ends.
