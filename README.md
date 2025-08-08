# mcp-memory-libsql-go

A Go implementation of the MCP Memory Server using libSQL for persistent storage with vector search capabilities.

## Overview

This project is a 1:1 feature port of the TypeScript `mcp-memory-libsql` project to Go. It provides a high-performance, persistent memory server for the Model Context Protocol (MCP) using libSQL (a fork of SQLite) for robust data storage, including vector search capabilities.

The go implemenation has a few advatages:

- 2x performance
- 40% less memory footprint
- single binary with no runtime dependencies
- tursodb/go-libsql driver
- multi-project support

## Features

- **Persistent Storage**: Uses libSQL for reliable data persistence
- **Vector Search**: Built-in cosine similarity search using libSQL's vector capabilities
- **MCP Integration**: Fully compatible with the Model Context Protocol
- **Knowledge Graph**: Store entities, observations, and relations
- **Multiple Database Support**: Works with local files and remote libSQL servers
- **Multi-Project Support**: Optionally, run in a mode that manages separate databases for multiple projects.

## Installation

To install the `mcp-memory-libsql-go` binary to a standard location on your system, use the following command:

```bash
make install
```

This will compile the binary and install it in a standard directory (e.g., `~/.local/bin` on Linux or `/usr/local/bin` on macOS), which should be in your system's `PATH`.

## Usage

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
- `EMBEDDING_DIMS`: Embedding dimension (default: `4`). Affects schema and vector operations.

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

Planned/Upcoming tools to reach parity with common client configurations:

- `add_observations`: Append observations to an existing entity
- `delete_entities`: Delete multiple entities by name (bulk)
- `delete_observations`: Delete observations by id or content
- `delete_relations` (bulk): Delete multiple relations
- `open_nodes`: Retrieve entities by names, with optional relations
- `update_entities`: Update entity metadata/embedding and manage observations (merge/replace)
- `update_relations`: Update relation tuples

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

Vector search input: The server accepts vector queries as JSON arrays (e.g., `[0.1, 0.2, 0.3, 0.4]`). Numeric strings like `"0.1"` are also accepted. The default embedding dimension is 4.

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
```

## Client Integration

### Cline

To use this server with Cline, you can add it to your MCP server configuration. This allows Cline to run the `mcp-memory-libsql-go` binary as a local MCP server using the stdio transport.

Here are some example configurations:

#### Single-Database Mode

This configuration runs the server with a single, specified database file.

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
        "delete_relation"
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

#### Multi-Project Mode

This configuration runs the server in multi-project mode, managing separate databases within a specified directory.

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
        "delete_relation"
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

Remember to replace `/path/to/some/dir/.memory/memory-bank` with the actual directory where you want per-project databases stored. The server will create subdirectories per project and a `libsql.db` inside each.

## Architecture

The project follows a clean, modular architecture:

- `main.go`: Application entry point
- `internal/apptype/`: Core data structures and MCP type definitions
- `internal/database/`: Database client and logic using libSQL
- `internal/server/`: MCP server implementation

## License

MIT
