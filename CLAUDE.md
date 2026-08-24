# go-rag-lab

Week 3 of a self-directed job-hunt technical lab (AI Engineering & RAG
Architecture, built in Go). See `~/.claude/skills/job-hunt-today/progress-log.md`
for the full week's schedule — this repo's day-1 code is scaffold +
Postgres/pgvector + ingestion pipeline only; retrieval API, chat endpoint,
and a client are later days and don't exist yet, don't assume they do.

## Commands

- `make build` / `make vet` / `make fmt` / `make lint` (= fmt-check + vet)
- `make test-unit` — pure logic + httptest-mocked Ollama, no infra needed
- `make test` — also runs DB integration tests, against `rag_test` (not
  dev's `rag` database — see below). Requires `make up` running.
- `make up` / `make down` — docker-compose's `db` service
- `make ingest` — runs the ingestion CLI natively against `sample_docs/`
- `make ci-verify` — CI only: brings up `db` + a pre-baked CI-only Ollama
  (see below), then builds and runs the `app` service against them for
  real. Not run by `make up` / local dev.
- `docker compose up --build app` — runs ingestion in the containerized
  path instead of natively, against your host-installed Ollama
- `scripts/ollama-dev --daemon` — installs Ollama if needed, ensures the
  systemd service is running, pulls the embedding model

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

`ingest.Store` is a small interface (not `*store.Store` directly) so tests
substitute a fake with no real Postgres involved — Go interfaces are
satisfied structurally, so `*store.Store` never had to declare it
implements anything.

## Conventions

- Stdlib-first: no `testify`, no CLI framework, no `godotenv`. Table-driven
  tests (`t.Run` subtests) are the default test shape.
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
- **`/etc/sudoers.d/ollama`** grants passwordless sudo for
  `systemctl start ollama` and `systemctl enable --now ollama` — Claude Code
  can run `scripts/ollama-dev --daemon` directly without needing a password.
  The file's `stop` entry has a typo (`systemctlstop`, missing a space) so
  `sudo systemctl stop ollama` is *not* covered and will still prompt
  interactively. No other sudo (apt installs, Docker, etc.) is passwordless
  here — those need the user to run the command themselves in their own
  terminal.
- Commands needing interactive human input — a sudo password prompt, `gh
  auth refresh`'s browser device-flow — will hang if run blind. Ask the
  user to run them directly instead.
