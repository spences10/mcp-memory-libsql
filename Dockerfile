# Build stage
FROM golang:1.24.3-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git build-base musl-dev gcc
COPY go.mod go.sum ./
RUN go mod tidy
COPY . .
# Build static binary
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /out/mcp-memory-libsql-go ./cmd/mcp-memory-libsql-go

# Base final stage with shared runtime configuration
FROM alpine:3.20 AS base
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/mcp-memory-libsql-go /usr/local/bin/mcp-memory-libsql-go
USER app

# Default environment variables
ENV LIBSQL_URL="file:/data/libsql.db" \
    LIBSQL_AUTH_TOKEN="" \
    EMBEDDING_DIMS=4 \
    DB_MAX_OPEN_CONNS="" \
    DB_MAX_IDLE_CONNS="" \
    DB_CONN_MAX_IDLE_SEC="" \
    DB_CONN_MAX_LIFETIME_SEC="" \
    METRICS_PROMETHEUS="" \
    METRICS_ADDR=":9090" \
    EMBEDDINGS_PROVIDER="" \
    HYBRID_SEARCH="" \
    HYBRID_TEXT_WEIGHT=0.4 \
    HYBRID_VECTOR_WEIGHT=0.6 \
    HYBRID_RRF_K=60 \
    OPENAI_API_KEY="" \
    OPENAI_EMBEDDINGS_MODEL="text-embedding-3-small" \
    OLLAMA_HOST="" \
    OLLAMA_EMBEDDINGS_MODEL="nomic-embed-text" \
    GOOGLE_API_KEY="" \
    GEMINI_EMBEDDINGS_MODEL="text-embedding-004" \
    VERTEX_EMBEDDINGS_ENDPOINT="" \
    VERTEX_ACCESS_TOKEN="" \
    LOCALAI_BASE_URL="http://localhost:8080/v1" \
    LOCALAI_EMBEDDINGS_MODEL="text-embedding-ada-002"

# Volumes and ports
VOLUME ["/data"]
EXPOSE 8080 9090

# Healthcheck hits metrics healthz if enabled, otherwise process check
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD wget -qO- http://127.0.0.1:9090/healthz || pgrep -x mcp-memory-libsql-go >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/mcp-memory-libsql-go"]

# Single DB SSE stage
FROM base AS single-db-sse
CMD ["-transport", "sse"]

# Single DB Stdio stage
FROM base AS single-db-stdio
CMD ["-transport", "stdio"]

# Multi-project SSE stage
FROM base AS multi-project-sse
CMD ["-transport", "sse", "-projects-dir", "/data/projects"]

# Multi-project Stdio stage
FROM base AS multi-project-stdio
CMD ["-transport", "stdio", "-projects-dir", "/data/projects"]