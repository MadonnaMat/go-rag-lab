# syntax=docker/dockerfile:1
#
# One shared build stage compiling all three binaries, then a thin final
# stage per binary — docker-compose's services select which one they get via
# `build.target`, so there's one Dockerfile (not one per binary) that can't
# drift out of sync with the others.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Regenerates docs/ fresh from source annotations, same as `make build`'s
# swagger prerequisite — Docker's build doesn't go through the Makefile, so
# it needs its own regeneration step here.
RUN go tool swag init -g cmd/serve/main.go -o docs
RUN CGO_ENABLED=0 go build -o /out/ingest ./cmd/ingest
RUN CGO_ENABLED=0 go build -o /out/serve ./cmd/serve
RUN CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12 AS ingest
WORKDIR /app
COPY --from=build /out/ingest /app/ingest
COPY sample_docs /app/sample_docs
ENTRYPOINT ["/app/ingest"]
CMD ["-dir=/app/sample_docs"]

FROM gcr.io/distroless/static-debian12 AS serve
WORKDIR /app
COPY --from=build /out/serve /app/serve
ENTRYPOINT ["/app/serve"]

FROM gcr.io/distroless/static-debian12 AS migrate
WORKDIR /app
COPY --from=build /out/migrate /app/migrate
ENTRYPOINT ["/app/migrate"]
