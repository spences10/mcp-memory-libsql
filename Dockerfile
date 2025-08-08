# We cannot use an Alpine image for building because the go-libsql package uses pre-compiled binaries that were built-against glibc

##### Build stage ###############################################################
FROM golang:1.24.3 AS build

WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git build-essential gcc g++ && \
    rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod tidy
COPY . .

# Build metadata
ARG VERSION=dev
ARG REVISION=dev
ARG BUILD_DATE

# Build binary with CGO (libsql requires glibc)
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -X github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/buildinfo.Version=${VERSION} -X github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/buildinfo.Revision=${REVISION} -X github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/buildinfo.BuildDate=${BUILD_DATE}" \
    -o /out/mcp-memory-libsql-go ./cmd/mcp-memory-libsql-go

# Base final stage with shared runtime configuration (glibc)
FROM debian:bookworm-slim AS base

RUN groupadd --system app && useradd --system --gid app --home /app --shell /usr/sbin/nologin app
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata curl procps && \
    rm -rf /var/lib/apt/lists/*

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
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD curl -fsS http://127.0.0.1:9090/healthz || pgrep -x mcp-memory-libsql-go >/dev/null || exit 1

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