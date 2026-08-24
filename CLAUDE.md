# go-rag-lab

Week 3 of a self-directed job-hunt technical lab (AI Engineering & RAG
Architecture, built in Go). See `~/.claude/skills/job-hunt-today/progress-log.md`
for the full week's schedule — this repo's day-1 code is scaffold +
Postgres/pgvector + ingestion pipeline only; retrieval API, chat endpoint,
and a client are later days and don't exist yet, don't assume they do.

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

## Commands

- `make build` / `make vet` / `make fmt` / `make lint` (= fmt-check + vet)
- `make test-unit` — pure logic + httptest-mocked Ollama, no infra needed
- `make test` — also runs DB integration tests, against `rag_test` (not
  dev's `rag` database — see below). Requires `make up` running.
- `make up` / `make down` — docker-compose's `db` service
- `make ingest` — runs the ingestion CLI natively against `sample_docs/`
- `make docker-build` — verifies the `Dockerfile` still builds (CI runs
  this; nothing else in CI touches Docker at all)
- `docker compose up --build app` — runs ingestion in the containerized
  path instead of natively
- `scripts/ollama-dev --daemon` — installs Ollama if needed, ensures the
  systemd service is running, pulls the embedding model

CI (`.github/workflows/ci.yml`) calls these same `make` targets — never a
hand-copied raw command — specifically so CI can't silently drift from what
running them locally actually does.

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
