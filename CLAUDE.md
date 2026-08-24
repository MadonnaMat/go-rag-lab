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

More to come here as the day's work continues (architecture, commands,
conventions).
