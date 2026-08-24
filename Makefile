.PHONY: build vet fmt fmt-check lint test test-unit up down ingest ci-verify

build:
	go build ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt: files need formatting" && exit 1)

lint: fmt-check vet

# Unit tests only: force DATABASE_URL empty for this invocation so DB tests
# self-skip via t.Skip, even if it's exported in the calling shell.
test-unit:
	DATABASE_URL= go test ./... -v

# Full suite: sources .env for TEST_DATABASE_URL, then runs tests against
# it (as DATABASE_URL) rather than dev's DATABASE_URL — so DB-gated tests
# never touch real ingested documents. Requires `make up` running.
test:
	@set -a; [ -f .env ] && . ./.env; set +a; DATABASE_URL="$$TEST_DATABASE_URL" go test ./... -v

up:
	docker compose up -d --wait db

down:
	docker compose down

ingest:
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/ingest -dir=sample_docs

# CI only: see scripts/ci-verify — brings up db + the pre-baked CI-only
# Ollama, runs the containerized app service against them twice, and
# asserts on the resulting database state directly (not just the exit
# code) — including that a re-run replaces rows rather than duplicating
# them. Not run by `make up` — local dev keeps using host-installed Ollama
# for GPU access instead (see CLAUDE.md).
ci-verify:
	./scripts/ci-verify
