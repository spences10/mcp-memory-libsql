<!-- [![MseeP.ai Security Assessment Badge](https://mseep.net/pr/ZanzyTHEbar-mcp-memory-libsql-go-badge.png)](https://mseep.ai/app/ZanzyTHEbar-mcp-memory-libsql-go) -->

# mcp-memory-libsql-go

A Go implementation of the MCP Memory Server using libSQL for persistent storage with vector search capabilities.

## Overview

This project started as a 1:1 feature port of the TypeScript `mcp-memory-libsql` project to Go. However, this project has since evolved to included much-needed improvements upon the original codebase.

`mcp-memory-libsql-go` provides a high-performance, persistent memory server for the Model Context Protocol (MCP) using libSQL (a fork of SQLite by Turso) for robust data storage, including vector search capabilities.

The go implemenation has a few advantages:

- 2x performance
- 40% less memory footprint
- single binary with no runtime dependencies
- tursodb/go-libsql driver
- multi-project support

And more!

## Features

- **Persistent Storage**: Uses libSQL for reliable data persistence
- **Vector Search**: Built-in cosine similarity search using libSQL's vector capabilities
- **Hybrid Search**: Leverages Semantic & Vector search using a postgres-inspired algorithm
- **MCP Integration**: Fully compatible with the Model Context Protocol, stdio & sse transports
- **Knowledge Graph**: Store entities, observations, and relations
- **Multiple Database Support**: Works with local files and remote libSQL servers
- **Multi-Project Support**: Optionally, run in a mode that manages separate databases for multiple projects.
- **Metrics (optional)**: No-op by default; enable Prometheus exporter with `METRICS_PROMETHEUS=true`

## Installation

To install the `mcp-memory-libsql-go` binary to a standard location on your system, use the following command:

```bash
make install
```

This will compile the binary and install it in a standard directory (e.g., `~/.local/bin` on Linux or `/usr/local/bin` on macOS), which should be in your system's `PATH`.

## Quick Start

### Local (stdio) – single database

```bash
# default local db at ./libsql.db
./mcp-memory-libsql-go

# or specify a file
./mcp-memory-libsql-go -libsql-url file:./my-memory.db
```

### Remote libSQL (stdio)

```bash
LIBSQL_URL=libsql://your-db.turso.io \
LIBSQL_AUTH_TOKEN=your-token \
./mcp-memory-libsql-go
```

### SSE transport (HTTP)

```bash
./mcp-memory-libsql-go -transport sse -addr :8080 -sse-endpoint /sse
# Connect with an SSE-capable MCP client to http://localhost:8080/sse
```

### Docker & Docker Compose (0→1 guide)

This section shows exactly how to get the server running in Docker, with or without docker-compose, and how to enable embeddings and hybrid search.

#### Prerequisites

- Docker (v20+) and Docker Compose (v2)
- Open ports: 8080 (SSE) and 9090 (metrics/health)
- Disk space for a mounted data volume

#### 1) Build the image

```bash
make docker
```

This builds `mcp-memory-libsql-go:local` and injects version metadata.

#### 2) Create a data directory

```bash
mkdir -p ./data
```

#### 3) Choose an embeddings provider (optional but recommended)

Set `EMBEDDINGS_PROVIDER` and provider-specific variables. For new databases, set `EMBEDDING_DIMS` to the desired embedding dimensionality. For existing databases, the server automatically detects the current DB dimensionality and adapts provider output vectors to match it (see “Embedding Dimensions” below). Common mappings are listed later in this README.

You can create a `.env` file for Compose or export env vars directly. Example `.env` for OpenAI:

```bash
cat > .env <<'EOF'
EMBEDDINGS_PROVIDER=openai
OPENAI_API_KEY=sk-...
OPENAI_EMBEDDINGS_MODEL=text-embedding-3-small
EMBEDDING_DIMS=1536
METRICS_PROMETHEUS=true
METRICS_ADDR=:9090
TRANSPORT=sse
ADDR=:8080
SSE_ENDPOINT=/sse
EOF
```

> [!IMPORTANT]
> Each database fixes its embedding size at creation (`F32_BLOB(N)`). The server now (1) detects the DB’s current size at startup and (2) automatically adapts provider outputs via padding/truncation so you can change provider/model without migrating the DB. To change the actual stored size, create a new DB (or run a manual migration) with a different `EMBEDDING_DIMS`.

#### 4) Run with docker-compose (recommended)

The repo includes a `docker-compose.yml` with profiles:

- `single` (default): single database at `/data/libsql.db`
- `multi`: multi-project mode at `/data/projects/<name>/libsql.db`
- `ollama`: optional Ollama sidecar
- `localai`: optional LocalAI sidecar (OpenAI-compatible)

Start single DB SSE server:

```bash
docker compose --profile single up --build -d
```

OpenAI quick start (using `.env` above):

```bash
docker compose --profile single up --build -d
```

Ollama quick start (sidecar):

```bash
cat > .env <<'EOF'
EMBEDDINGS_PROVIDER=ollama
OLLAMA_HOST=http://ollama:11434
EMBEDDING_DIMS=768
TRANSPORT=sse
# Optional: increase timeout to allow cold model load
OLLAMA_HTTP_TIMEOUT=60s
EOF

docker compose --profile ollama --profile single up --build -d
```

LocalAI quick start (sidecar):

```bash
cat > .env <<'EOF'
EMBEDDINGS_PROVIDER=localai
LOCALAI_BASE_URL=http://localai:8080/v1
LOCALAI_EMBEDDINGS_MODEL=text-embedding-ada-002
EMBEDDING_DIMS=1536
TRANSPORT=sse
EOF

docker compose --profile localai --profile single up --build -d
```

Multi-project mode:

```bash
docker compose --profile multi up --build -d
# exposes on 8081/9091 by default per compose file
```

When Multi-Project Mode is enabled:

- All tool calls MUST include `projectArgs.projectName`.
- Per-project auth: include `projectArgs.authToken`. On first use, the token is persisted at `<ProjectsDir>/<projectName>/.auth_token` (0600). Subsequent calls must present the same token.
- Calls without `projectName` or with invalid tokens are rejected. You can relax this by setting `MULTI_PROJECT_AUTH_REQUIRED=false` (see below). You can also enable automatic token initialization with `MULTI_PROJECT_AUTO_INIT_TOKEN=true` and optionally provide `MULTI_PROJECT_DEFAULT_TOKEN`.

Health and metrics:

```bash
curl -fsS http://localhost:9090/healthz
curl -fsS http://localhost:9090/metrics | head -n 20
```

Stop and clean up:

```bash
docker compose down
# remove volumes only if you want to delete your data
docker compose down -v
```

#### 5) Alternative: plain docker run

```bash
docker run --rm -p 8080:8080 -p 9090:9090 \
  -e METRICS_PROMETHEUS=true -e METRICS_ADDR=":9090" \
  -e EMBEDDING_DIMS=768 \
  -v $(pwd)/data:/data \
  mcp-memory-libsql-go:local -transport sse -addr :8080 -sse-endpoint /sse
```

#### Remote libSQL (optional)

Point to a remote libSQL instance:

```bash
export LIBSQL_URL=libsql://your-db.turso.io
export LIBSQL_AUTH_TOKEN=your-token
docker compose --profile single up --build -d
```

If you later change `EMBEDDING_DIMS`, it will not alter an existing DB’s schema. The server will continue to adopt the DB’s actual size. To change sizes, create a new DB or migrate.

#### Example (Go) SSE client

```go
package main

import (
  "context"
  "log"
  "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
  ctx := context.Background()
  client := mcp.NewClient(&mcp.Implementation{Name: "example-client", Version: "dev"}, nil)
  transport := mcp.NewSSEClientTransport("http://localhost:8080/sse", nil)
  session, err := client.Connect(ctx, transport)
  if err != nil { log.Fatal(err) }
  defer session.Close()

  tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
  if err != nil { log.Fatal(err) }
  for _, t := range tools.Tools { log.Println("tool:", t.Name) }
}
```

### Multi-project mode

```bash
mkdir -p /path/to/projects
./mcp-memory-libsql-go -projects-dir /path/to/projects
# Databases will be created under /path/to/projects/<projectName>/libsql.db
```

### Configure embedding dimensions

```bash
EMBEDDING_DIMS=1536 ./mcp-memory-libsql-go  # create a fresh DB with 1536-dim embeddings
```

> [!NOTE]
> Changing `EMBEDDING_DIMS` for an existing DB requires a manual migration or new DB file.

## Usage

### Prompts

This server registers MCP prompts to guide knowledge graph operations:

- `quick_start`: Quick guidance for using tools (search, read, edit)
- `search_nodes_guidance(query, limit?, offset?)`: Compose effective searches with pagination
- `kg_init_new_repo(repoSlug, areas?, includeIssues?)`: Initialize an optimal KG for a new repository
- `kg_update_graph(targetNames, replaceObservations?, mergeObservations?, newRelations?, removeRelations?)`: Update entities/relations idempotently
- `kg_sync_github(tasks, canonicalUrls?)`: Ensure exactly one canonical `GitHub:` observation per `Task:*`
- `kg_read_best_practices(query, limit?, offset?, expand?, direction?)`: Best-practices layered graph reading

Notes:

- Prompts return structured descriptions of recommended tool sequences.
- Follow the recommended order to maintain idempotency and avoid duplicates.
- Text search gracefully falls back to LIKE when FTS5 is unavailable; vector search falls back when vector_top_k is missing.
- Query language highlights for `search_nodes` (text):
  - FTS first, LIKE fallback; tokenizer includes `:` `-` `_` `@` `.` `/`.
  - Prefix: append `*` to a token (e.g., `Task:*`). Recommended token length ≥ 2.
  - Field qualifiers (FTS only): `entity_name:` and `content:` (e.g., `entity_name:"Repo:"* OR content:"P0"`).
  - Phrases: `"exact phrase"`. Boolean OR supported (space implies AND).
  - Special: `Task:*` is treated as a prefix on the literal `Task:` token across both entity name and content.
  - On FTS parse errors (e.g., exotic syntax), the server auto-downgrades to LIKE and normalizes `*` → `%`.
  - Ranking: when FTS is active, results are ranked by BM25 if the function is available; otherwise ordered by `e.name`. BM25 can be disabled or tuned via environment (see below).

Examples:

```json
{ "query": "Task:*", "limit": 10 }
```

```json
{ "query": "entity_name:\"Repo:\"* OR content:\"P0\"" }
```

```json
{ "query": "\"design decision\"", "limit": 5 }
```

### Using Prompts with MCP Clients

#### What prompts are

- Prompts are named, parameterized templates you can fetch from the server. They return guidance (and example JSON plans) describing which tools to call and with what arguments.
- Prompts do not execute actions themselves. Your client still calls tools like `create_entities`, `search_nodes`, etc., using the plan returned by the prompt.

#### Workflow

- List prompts: `ListPrompts`
- Retrieve a prompt: `GetPrompt(name, arguments)`
- Parse the returned description for the JSON tool plan and follow it to execute tool calls (via `CallTool`).

#### Minimal Go example

```go
ctx := context.Background()
client := mcp.NewClient(&mcp.Implementation{Name: "prompt-client", Version: "dev"}, nil)
transport := mcp.NewSSEClientTransport("http://localhost:8080/sse", nil)
session, _ := client.Connect(ctx, transport)
defer session.Close()

// 1) List available prompts
plist, _ := session.ListPrompts(ctx, &mcp.ListPromptsParams{})
for _, p := range plist.Prompts { log.Println("prompt:", p.Name) }

// 2) Retrieve a prompt with arguments (e.g., KG init)
pr, _ := session.GetPrompt(ctx, &mcp.GetPromptParams{
  Name: "kg_init_new_repo",
  Arguments: map[string]any{
    "repoSlug": "owner/repo",
    "areas":    []string{"database","server"},
  },
})
log.Println("description:\n", pr.Description) // contains JSON tool plan + Mermaid

// 3) Execute the plan (example create_entities call)
raw := json.RawMessage(`{"projectArgs":{"projectName":"default"},"entities":[{"name":"Repo: owner/repo","entityType":"Repo","observations":["Primary repository for KG"]}]}`)
_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "create_entities", Arguments: raw})
```

> Tip: Render the prompt description as Markdown to view Mermaid diagrams and copy the embedded JSON plan.

### Command-line Flags

- `-libsql-url`: Database URL (default: `file:./libsql.db`). Overrides the `LIBSQL_URL` environment variable.
- `-auth-token`: Authentication token for remote databases. Overrides the `LIBSQL_AUTH_TOKEN` environment variable.
- `-projects-dir`: Base directory for projects. Enables multi-project mode. If this is set, `-libsql-url` is ignored.
- `-transport`: Transport to use: `stdio` (default) or `sse`.
- `-addr`: Address to listen on when using SSE transport (default `:8080`).
- `-sse-endpoint`: SSE endpoint path when using SSE transport (default `/sse`).

### Environment Variables

- `LIBSQL_URL`: Database URL (default: `file:./libsql.db`)
  - Local file: `file:./path/to/db.sqlite`
  - Remote libSQL: `libsql://your-db.turso.io`
- `LIBSQL_AUTH_TOKEN`: Authentication token for remote databases
- `EMBEDDING_DIMS`: Embedding dimension for new databases (default: `4`). Existing DBs are auto-detected and take precedence at runtime.
- `EMBEDDINGS_ADAPT_MODE`: How to adapt provider vectors to the DB size: `pad_or_truncate` (default) | `pad` | `truncate`.
- `PROJECTS_DIR`: Base directory for multi-project mode (can also be set via flag `-projects-dir`).
- `MULTI_PROJECT_AUTH_REQUIRED`: Set to `false`/`0` to disable per-project auth enforcement (default: required).
- `MULTI_PROJECT_AUTO_INIT_TOKEN`: Set to `true`/`1` to auto-create a token file on first access when none exists; the first call will fail with an instruction to retry with the token.
- `MULTI_PROJECT_DEFAULT_TOKEN`: Optional token value used when auto-initializing; if omitted, a random token is generated.
- `DB_MAX_OPEN_CONNS`: Max open DB connections (optional)
- `DB_MAX_IDLE_CONNS`: Max idle DB connections (optional)
- `DB_CONN_MAX_IDLE_SEC`: Connection max idle time in seconds (optional)
- `DB_CONN_MAX_LIFETIME_SEC`: Connection max lifetime in seconds (optional)
- `METRICS_PROMETHEUS`: If set (e.g., `true`), expose Prometheus metrics
- `METRICS_ADDR`: Metrics HTTP address (default `:9090`) exposing `/metrics` and `/healthz`
- `EMBEDDINGS_PROVIDER`: Optional embeddings source. Supported values and aliases:
  - `openai`
  - `ollama`
  - `gemini` | `google` | `google-gemini` | `google_genai`
  - `vertexai` | `vertex` | `google-vertex`
  - `localai` | `llamacpp` | `llama.cpp`
  - `voyageai` | `voyage` | `voyage-ai`
    The server still accepts client-supplied embeddings if unset.
- Hybrid Search (optional):
  - `HYBRID_SEARCH` (true/1 to enable)
  - `HYBRID_TEXT_WEIGHT` (default 0.4)
  - `HYBRID_VECTOR_WEIGHT` (default 0.6)
  - `HYBRID_RRF_K` (default 60)
  - Text ranking (BM25 for FTS):
    - `BM25_ENABLE` (default true). Set to `false` or `0` to disable BM25 ordering.
    - `BM25_K1` (optional) — saturation parameter. Example `1.2`.
    - `BM25_B` (optional) — length normalization parameter. Example `0.75`.
    - If `BM25_K1` and `BM25_B` are both set, the server uses `bm25(table,k1,b)`; otherwise it uses `bm25(table)`.
- OpenAI: `OPENAI_API_KEY`, `OPENAI_EMBEDDINGS_MODEL` (default `text-embedding-3-small`).
- Ollama: `OLLAMA_HOST`, `OLLAMA_EMBEDDINGS_MODEL` (default `nomic-embed-text`, dims 768). Example `OLLAMA_HOST=http://localhost:11434`.
- Google Gemini (Generative Language API): `GOOGLE_API_KEY`, `GEMINI_EMBEDDINGS_MODEL` (default `text-embedding-004`, dims 768).
- Google Vertex AI: `VERTEX_EMBEDDINGS_ENDPOINT`, `VERTEX_ACCESS_TOKEN` (Bearer token). Endpoint format: `https://{location}-aiplatform.googleapis.com/v1/projects/{project}/locations/{location}/publishers/google/models/{model}:predict`.
- LocalAI / llama.cpp (OpenAI-compatible): `LOCALAI_BASE_URL` (default `http://localhost:8080/v1`), `LOCALAI_EMBEDDINGS_MODEL` (default `text-embedding-ada-002`, dims 1536), optional `LOCALAI_API_KEY`.
- VoyageAI: `VOYAGEAI_API_KEY` (or `VOYAGE_API_KEY`), `VOYAGEAI_EMBEDDINGS_MODEL` (default `voyage-3-lite`). Optional `VOYAGEAI_EMBEDDINGS_DIMS` to explicitly set expected output length if you need to override.

> [!IMPORTANT]
> Provider outputs are automatically adapted to the DB’s fixed embedding size (padding/truncation). This allows switching providers/models without recreating the DB. Your client-supplied vector queries must still be exactly the DB size. Use the `health_check` tool to see the current `EmbeddingDims`.

### Hybrid Search

Hybrid Search fuses text results (FTS5 when available, otherwise `LIKE`) with vector similarity using an RRF-style scoring function:

- Score = `HYBRID_TEXT_WEIGHT * (1/(k + text_rank)) + HYBRID_VECTOR_WEIGHT * (1/(k + vector_rank))`
- Defaults: text=0.4, vector=0.6, k=60
- Requires an embeddings provider to generate a vector for the text query. If unavailable or dims mismatch, hybrid degrades to text-only.
- If FTS5 is not available, the server falls back to `LIKE` transparently.
- When FTS is active, the text-side rank uses BM25 (if available) for higher-quality ordering; otherwise it uses name ordering.

Enable and tune:

```bash
HYBRID_SEARCH=true \
HYBRID_TEXT_WEIGHT=0.4 HYBRID_VECTOR_WEIGHT=0.6 HYBRID_RRF_K=60 \
EMBEDDINGS_PROVIDER=openai OPENAI_API_KEY=... OPENAI_EMBEDDINGS_MODEL=text-embedding-3-small \
EMBEDDING_DIMS=1536 \
./mcp-memory-libsql-go
```

#### Common model → EMBEDDING_DIMS mapping

| Provider | Model                     | Dimensions | Set `EMBEDDING_DIMS`  |
| -------: | ------------------------- | ---------- | --------------------- |
|   OpenAI | `text-embedding-3-small`  | 1536       | 1536                  |
|   OpenAI | `text-embedding-3-large`  | 3072       | 3072                  |
|   Ollama | `nomic-embed-text`        | 768        | 768                   |
|   Gemini | `text-embedding-004`      | 768        | 768                   |
| VertexAI | `textembedding-gecko@003` | 768        | 768                   |
|  LocalAI | `text-embedding-ada-002`  | 1536       | 1536                  |
| VoyageAI | `voyage-3-*`              | varies     | Set once at DB create |

> ![IMPORTANT]
> Verify your exact model’s dimensionality with a quick API call (examples below) and set `EMBEDDING_DIMS` accordingly before creating a new DB.

#### Provider quick verification (curl / Go)

These calls help you confirm the embedding vector length (dimension) for your chosen model.

OpenAI

```bash
curl -s \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  https://api.openai.com/v1/embeddings \
  -d '{"model":"text-embedding-3-small","input":["hello","world"]}' \
| jq '.data[0].embedding | length'
```

Ollama (v0.2.6+ embeds endpoint)

```bash
curl -s "$OLLAMA_HOST/api/embed" \
  -H "Content-Type: application/json" \
  -d '{"model":"nomic-embed-text","input":["hello","world"]}' \
| jq '.embeddings[0] | length'
```

Notes:

- The entrypoint no longer calls `ollama run` for the embedding model; Ollama will lazily load on first `/api/embed` call.
- You can tune the client timeout via `OLLAMA_HTTP_TIMEOUT` (e.g. `30s`, `60s`, or integer seconds like `90`).

Gemini (Generative Language API)

```bash
curl -s \
  -H "Content-Type: application/json" \
  "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:embedContent?key=$GOOGLE_API_KEY" \
  -d '{"content":{"parts":[{"text":"hello"}]}}' \
| jq '.embedding.values | length'
```

Vertex AI (using gcloud for access token)

```bash
export PROJECT_ID="your-project" LOCATION="us-central1"
export MODEL="textembedding-gecko@003"
export ENDPOINT="https://$LOCATION-aiplatform.googleapis.com/v1/projects/$PROJECT_ID/locations/$LOCATION/publishers/google/models/$MODEL:predict"
export TOKEN="$(gcloud auth print-access-token)"

curl -s "$ENDPOINT" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"instances":[{"content":"hello"}]}' \
| jq '.predictions[0].embeddings.values | length'
```

LocalAI (OpenAI-compatible)
VoyageAI (Go SDK)

```go
package main
import (
  "fmt"
  voyageai "github.com/austinfhunter/voyageai"
)
func main() {
  vo := voyageai.NewClient(&voyageai.VoyageClientOpts{Key: os.Getenv("VOYAGEAI_API_KEY")})
  resp, _ := vo.Embed([]string{"hello"}, "voyage-3-lite", nil)
  fmt.Println(len(resp.Data[0].Embedding)) // vector length
}
```

```bash
curl -s "$LOCALAI_BASE_URL/embeddings" \
  -H "Content-Type: application/json" \
  -d '{"model":"text-embedding-ada-002","input":["hello","world"]}' \
| jq '.data[0].embedding | length'
```

### Running the Server

#### Single Database Mode

```bash
# Using default local database
./mcp-memory-libsql-go

# Using a specific local database file
./mcp-memory-libsql-go -libsql-url file:./my-memory.db

# Using environment variables for a remote database
LIBSQL_URL=libsql://your-db.turso.io LIBSQL_AUTH_TOKEN=your-token ./mcp-memory-libsql-go
```

#### Multi-Project Mode

When running in multi-project mode, the server will create a subdirectory for each project within the specified projects directory. Each subdirectory will contain a `libsql.db` file.

```bash
# Run in multi-project mode
./mcp-memory-libsql-go -projects-dir /path/to/projects
```

## Tools Provided

The server provides the following MCP tools:

- `create_entities`: Create new entities with observations and optional embeddings
- `search_nodes`: Search for entities and their relations using text or vector similarity
- `read_graph`: Get recent entities and their relations
- `create_relations`: Create relations between entities
- `delete_entity`: Delete an entity and all its associated data
- `delete_relation`: Delete a specific relation between entities
- `add_observations`: Append observations to an existing entity
- `open_nodes`: Retrieve entities by names with optional relations
- `delete_entities`: Delete multiple entities by name (bulk)
- `delete_observations`: Delete observations by id/content or all for an entity
- `delete_relations`: Delete multiple relations (bulk)
- `update_entities`: Update entity metadata/embedding and manage observations (merge/replace)
- `update_relations`: Update relation tuples
- `health_check`: Return server info and configuration
- `neighbors`: 1-hop neighbors for given entities (direction out|in|both)
- `walk`: bounded-depth graph walk from seeds (direction/limit)
- `shortest_path`: shortest path between two entities

### Tool Summary

| Tool                | Purpose                                 | Required args                 | Optional args                                   | Notes                                       |
| ------------------- | --------------------------------------- | ----------------------------- | ----------------------------------------------- | ------------------------------------------- |
| create_entities     | Create/update entities and observations | `entities[]`                  | `projectArgs`                                   | Replaces observations for provided entities |
| search_nodes        | Text or vector search                   | `query`                       | `projectArgs`, `limit`, `offset`                | Query is string or numeric array            |
| read_graph          | Recent entities + relations             | –                             | `projectArgs`, `limit`                          | Default limit 10                            |
| create_relations    | Create relations                        | `relations[]`                 | `projectArgs`                                   | Inserts source→target with type             |
| delete_entity       | Delete entity + all data                | `name`                        | `projectArgs`                                   | Cascades to observations/relations          |
| delete_relation     | Delete a relation                       | `source`,`target`,`type`      | `projectArgs`                                   | Removes one tuple                           |
| add_observations    | Append observations                     | `entityName`,`observations[]` | `projectArgs`                                   | Does not replace existing                   |
| open_nodes          | Get entities by names                   | `names[]`                     | `projectArgs`, `includeRelations`               | Fetch relations for returned set            |
| delete_entities     | Bulk delete entities                    | `names[]`                     | `projectArgs`                                   | Transactional bulk delete                   |
| delete_observations | Delete observations                     | `entityName`                  | `projectArgs`, `ids[]`, `contents[]`            | If neither provided, deletes all for entity |
| delete_relations    | Bulk delete relations                   | `relations[]`                 | `projectArgs`                                   | Transactional bulk delete                   |
| update_entities     | Partial entity update                   | `updates[]`                   | `projectArgs`                                   | Update type/embedding/observations          |
| update_relations    | Update relation tuples                  | `updates[]`                   | `projectArgs`                                   | Delete old + insert new tuple               |
| health_check        | Server health/info                      | –                             | –                                               | Version, revision, build date, dims         |
| neighbors           | 1-hop neighbors                         | `names[]`                     | `projectArgs`, `direction`, `limit`             | direction: out/in/both (default both)       |
| walk                | Graph expansion (BFS)                   | `names[]`                     | `projectArgs`, `maxDepth`, `direction`, `limit` | Bounded-depth walk                          |
| shortest_path       | Shortest path                           | `from`,`to`                   | `projectArgs`, `direction`                      | Returns path entities and edges             |

#### Metrics

- Set `METRICS_PROMETHEUS=true` to expose `/metrics` and `/healthz` on `METRICS_ADDR` (default `:9090`).
- DB hot paths and tool handlers are instrumented with counters and latency histograms.
- Additional gauges and counters:
  - `db_pool_gauges{state="in_use|idle"}` observed periodically and on `health_check`
  - `stmt_cache_events_total{op="prepare",result="hit|miss"}` from the prepared statement cache

Recommended Prometheus histogram buckets (example):

```
# scrape_config for reference only
histogram_quantile(0.50, sum(rate(tool_call_seconds_bucket[5m])) by (le, tool))
histogram_quantile(0.90, sum(rate(tool_call_seconds_bucket[5m])) by (le, tool))
histogram_quantile(0.99, sum(rate(tool_call_seconds_bucket[5m])) by (le, tool))
```

- If metrics are disabled, a no-op implementation is used.

> We keep this table and examples up to date as the project evolves. If anything is missing or incorrect, please open an issue or PR.

Planned/Upcoming tools:

– (none for now) –

### Using Tools in Multi-Project Mode

When in multi-project mode, all tools accept an optional project context under `projectArgs.projectName`. If not provided, the server uses the "default" project.

**Example `create_entities` call:**

```json
{
  "tool_name": "create_entities",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "entities": [
      {
        "name": "entity-1",
        "entityType": "type-a",
        "observations": ["obs1"]
      }
    ]
  }
}
```

**Example `search_nodes` (text) call:**

```json
{
  "tool_name": "search_nodes",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "query": "apple"
  }
}
```

**Example `search_nodes` (vector) call (4D default):**

```json
{
  "tool_name": "search_nodes",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "query": [0.1, 0.2, 0.3, 0.4]
  }
}
```

Pagination parameters:

- `limit` (optional): maximum number of results (default 5 for `search_nodes`, 10 for `read_graph`)
- `offset` (optional): number of results to skip (for paging)

**Example `delete_entities` (bulk) call:**

```json
{
  "tool_name": "delete_entities",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "names": ["entity-1", "entity-2"]
  }
}
```

**Example `delete_relations` (bulk) call:**

```json
{
  "tool_name": "delete_relations",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "relations": [{ "from": "a", "to": "b", "relationType": "connected_to" }]
  }
}
```

**Example `delete_observations` call:**

```json
{
  "tool_name": "delete_observations",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "entityName": "entity-1",
    "ids": [1, 2],
    "contents": ["exact observation text"]
  }
}
```

**Example `update_entities` call:**

```json
{
  "tool_name": "update_entities",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "updates": [
      {
        "name": "entity-1",
        "entityType": "type-b",
        "embedding": [0.1, 0.2, 0.3, 0.4],
        "mergeObservations": ["added obs"],
        "replaceObservations": []
      },
      {
        "name": "entity-2",
        "replaceObservations": ["only this obs"]
      }
    ]
  }
}
```

**Example `update_relations` call:**

```json
{
  "tool_name": "update_relations",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "updates": [
      {
        "from": "a",
        "to": "b",
        "relationType": "r1",
        "newTo": "c",
        "newRelationType": "r2"
      }
    ]
  }
}
```

**Example `health_check` call:**

```json
{
  "tool_name": "health_check",
  "arguments": {}
}
```

**Example `add_observations` call:**

```json
{
  "tool_name": "add_observations",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "entityName": "entity-1",
    "observations": ["new observation 1", "new observation 2"]
  }
}
```

**Example `open_nodes` call:**

```json
{
  "tool_name": "open_nodes",
  "arguments": {
    "projectArgs": { "projectName": "my-awesome-project" },
    "names": ["entity-1", "entity-2"],
    "includeRelations": true
  }
}
```

Vector search input: The server accepts vector queries as JSON arrays (e.g., `[0.1, 0.2, 0.3, 0.4]`). Numeric strings like `"0.1"` are also accepted. The default embedding dimension is 4 (configurable via `EMBEDDING_DIMS`).

### Embedding Dimensions

The embedding column is `F32_BLOB(N)`, fixed per database. On startup, the server detects the DB’s `N` and sets runtime behavior accordingly, adapting provider outputs via padding/truncation. Changing `EMBEDDING_DIMS` does not modify an existing DB; to change `N`, create a new DB (or migrate). Use the `health_check` tool to view the active `EmbeddingDims`.

### Transports: stdio and SSE

This server supports both stdio transport (default) and SSE transport. Use `-transport sse -addr :8080 -sse-endpoint /sse` to run an SSE endpoint. Clients must use an SSE-capable MCP client (e.g., go-sdk `SSEClientTransport`) to connect.

## Development

### Prerequisites

- Go 1.21 or later
- libSQL CGO dependencies (automatically handled by go-libsql)

### Building

```bash
go build .
```

### Testing

```bash
go test ./...

# Optional race detector
go test -race ./...

# Optional fuzz target (requires Go 1.18+)
go test -run=Fuzz -fuzz=Fuzz -fuzztime=2s ./internal/database
```

## Client Integration

This server supports both stdio and SSE transports and can run as:

- a raw binary (local stdio or SSE)
- a single Docker container (stdio or SSE)
- a Docker Compose stack (SSE, with multi-project mode and optional embeddings)

Below are reference integrations for Cursor/Cline and other MCP-ready clients.

### Cursor / Cline (MCP) via stdio (single DB)

```json
{
  "mcpServers": {
    "memory-db": {
      "autoApprove": [
        "create_entities",
        "search_nodes",
        "read_graph",
        "create_relations",
        "delete_entities",
        "delete_relations",
        "delete_entity",
        "delete_relation",
        "add_observations",
        "open_nodes",
        "delete_observations",
        "update_entities",
        "update_relations",
        "health_check",
        "neighbors",
        "walk",
        "shortest_path"
      ],
      "disabled": false,
      "timeout": 60,
      "type": "stdio",
      "command": "mcp-memory-libsql-go",
      "args": ["-libsql-url", "file:./my-memory.db"]
    }
  }
}
```

### Cursor / Cline (MCP) via stdio (multi-project)

```json
{
  "mcpServers": {
    "multi-project-memory-db": {
      "autoApprove": [
        "create_entities",
        "search_nodes",
        "read_graph",
        "create_relations",
        "delete_entities",
        "delete_relations",
        "delete_entity",
        "delete_relation",
        "add_observations",
        "open_nodes",
        "delete_observations",
        "update_entities",
        "update_relations",
        "health_check",
        "neighbors",
        "walk",
        "shortest_path"
      ],
      "disabled": false,
      "timeout": 60,
      "type": "stdio",
      "command": "mcp-memory-libsql-go",
      "args": ["-projects-dir", "/path/to/some/dir/.memory/memory-bank"]
    }
  }
}
```

> Replace `/path/to/some/dir/.memory/memory-bank` with your desired base directory. The server will create `/path/to/.../<projectName>/libsql.db` per project.

### Cursor / Cline (MCP) via SSE (Docker Compose, recommended for embeddings)

Run the Compose stack in multi-project mode with Ollama embeddings (hybrid search, pooling, metrics):

```bash
make prod
# SSE endpoint: http://localhost:8081/sse
```

Cursor/Cline SSE config:

```json
{
  "mcpServers": {
    "memory-db": {
      "autoApprove": [
        "create_entities",
        "search_nodes",
        "read_graph",
        "create_relations",
        "delete_entities",
        "delete_relations",
        "delete_entity",
        "delete_relation",
        "add_observations",
        "open_nodes",
        "delete_observations",
        "update_entities",
        "update_relations",
        "health_check",
        "neighbors",
        "walk",
        "shortest_path"
      ],
      "disabled": false,
      "timeout": 60,
      "type": "sse",
      "url": "http://localhost:8081/sse"
    }
  }
}
```

### Other usage patterns

- Raw binary (stdio):
  ```bash
  ./mcp-memory-libsql-go -libsql-url file:./libsql.db
  ```
- Raw binary (SSE):
  ```bash
  ./mcp-memory-libsql-go -transport sse -addr :8080 -sse-endpoint /sse
  # SSE URL: http://localhost:8080/sse
  ```
- Docker run (SSE):
  ```bash
  docker run --rm -p 8080:8080 -p 9090:9090 \
    -e METRICS_PROMETHEUS=true -e METRICS_ADDR=":9090" \
    -e EMBEDDING_DIMS=768 \
    -v $(pwd)/data:/data \
    mcp-memory-libsql-go:local -transport sse -addr :8080 -sse-endpoint /sse
  ```
- Docker Compose (single DB):
  ```bash
  docker compose --profile single up --build -d
  # SSE URL: http://localhost:8080/sse, Metrics: http://localhost:9090/healthz
  ```
- Docker Compose (multi-project, Ollama, hybrid):
  ```bash
  make prod
  # SSE URL: http://localhost:8081/sse, Metrics: http://localhost:9091/healthz
  ```

## Architecture

The project follows a clean, modular architecture:

- `main.go`: Application entry point
- `internal/apptype/`: Core data structures and MCP type definitions
- `internal/database/`: Database client and logic using libSQL
- `internal/server/`: MCP server implementation
- `internal/embeddings/`: Embeddings Providers implementations

## License

MIT
