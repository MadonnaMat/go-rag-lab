.PHONY: build vet fmt fmt-check lint test test-unit up down ingest migrate migrate-down swagger dev-up ci-verify

# Loads .env (if present) as real Make variables, exported to every
# recipe's environment — a single source instead of each target having to
# source .env itself in its own shell.
ifneq (,$(wildcard .env))
include .env
export
endif

# docs/ is generated (gitignored, not committed — see CLAUDE.md's "API
# docs" section) and cmd/serve blank-imports it, so every target that
# compiles anything depends on swagger first — otherwise a bare `go build`/
# `vet`/`test` on a fresh clone (bypassing make entirely) would be the only
# way to hit a "package not found" error.
build: swagger
	go build ./...

# Regenerates docs/ (docs.go + swagger.json + swagger.yaml) from the
# @-annotations in cmd/serve/main.go and internal/api/api.go, via the swag
# tool tracked in go.mod's `tool` directive (Go 1.24+) — no separate global
# install, resolved the same way any other module dependency is.
swagger:
	go tool swag init -g cmd/serve/main.go -o docs

vet: swagger
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt: files need formatting" && exit 1)

lint: fmt-check vet

# Unit tests only: force DATABASE_URL empty for this invocation so DB tests
# self-skip via t.Skip, even if it's exported in the calling shell.
test-unit: swagger
	DATABASE_URL= go test ./... -v

# Full suite: runs tests against TEST_DATABASE_URL (as DATABASE_URL)
# rather than dev's DATABASE_URL — so DB-gated tests never touch real
# ingested documents. Requires `make up` running. Applies migrations first
# so this stays a single command in CI, with no separate "migrate the test
# database" step to remember.
test: swagger
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate
	DATABASE_URL="$(TEST_DATABASE_URL)" go test ./... -v

up:
	docker compose up -d --wait db

down:
	docker compose down

# Schema setup is now the migration runner's job alone (see
# internal/store/migrations) — run this once against a fresh database
# before `make ingest` or `cmd/serve` will work. Not chained to `ingest`
# automatically: applying migrations is a deliberate, explicit step, not
# something ingestion/serving silently does on your behalf every time they
# start.
migrate:
	go run ./cmd/migrate

migrate-down:
	go run ./cmd/migrate -down

ingest:
	go run ./cmd/ingest -dir=sample_docs

# Quick local-verification loop: starts Ollama + db (only if not already
# running), migrates + ingests sample_docs, then runs cmd/serve in the
# foreground — go check the browser, Ctrl-C to stop everything this
# started. See scripts/dev-up.
dev-up:
	./scripts/dev-up

# CI only: see scripts/ci-verify — brings up db + the pre-baked CI-only
# Ollama, runs the containerized app service against them twice, and
# asserts on the resulting database state directly (not just the exit
# code) — including that a re-run replaces rows rather than duplicating
# them. Not run by `make up` — local dev keeps using host-installed Ollama
# for GPU access instead (see CLAUDE.md).
ci-verify:
	./scripts/ci-verify
