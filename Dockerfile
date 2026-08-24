# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ingest ./cmd/ingest

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/ingest /app/ingest
COPY sample_docs /app/sample_docs
ENTRYPOINT ["/app/ingest"]
CMD ["-dir=/app/sample_docs"]
