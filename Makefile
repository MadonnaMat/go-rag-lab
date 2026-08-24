.PHONY: build vet fmt fmt-check lint test test-unit up down ingest docker-build

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
	docker compose up -d db

down:
	docker compose down

ingest:
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/ingest -dir=sample_docs

# Verifies the Dockerfile itself still builds — CI runs this so a broken
# Dockerfile (bad COPY path, bad build-stage reference) fails the build
# rather than going unnoticed, since the Go build/test steps never touch
# it. Doesn't run the resulting image — that needs live Ollama, which CI
# deliberately doesn't have (see internal/embedding's httptest-only tests).
docker-build:
	docker build -t go-rag-lab .
