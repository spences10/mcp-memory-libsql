# mcp-memory-libsql-go

A Go implementation of the MCP Memory Server using libSQL for persistent storage with vector search capabilities.

## Overview

This project is a 1:1 port of the TypeScript `mcp-memory-libsql` project to Go. It provides a high-performance, persistent memory server for the Model Context Protocol (MCP) using libSQL (a fork of SQLite) for robust data storage, including vector search capabilities.

## Features

- **Persistent Storage**: Uses libSQL for reliable data persistence
- **Vector Search**: Built-in cosine similarity search using libSQL's vector capabilities
- **MCP Integration**: Fully compatible with the Model Context Protocol
- **Knowledge Graph**: Store entities, observations, and relations
- **Multiple Database Support**: Works with local files and remote libSQL servers

## Installation

```bash
make deps
make build
```

Or manually:

```bash
go mod tidy
go build -o mcp-memory-libsql-go ./cmd/mcp-memory-libsql
```

## Usage

### Environment Variables

- `LIBSQL_URL`: Database URL (default: `file:./memory-tool.db`)
  - Local file: `file:./path/to/db.sqlite`
  - Remote libSQL: `libsql://your-db.turso.io`
- `LIBSQL_AUTH_TOKEN`: Authentication token for remote databases

### Running the Server

```bash
# Using default local database
./mcp-memory-libsql-go

# Using environment variables
LIBSQL_URL=file:./my-memory.db ./mcp-memory-libsql-go

# Using remote database
LIBSQL_URL=libsql://your-db.turso.io LIBSQL_AUTH_TOKEN=your-token ./mcp-memory-libsql-go
```

## Tools Provided

The server provides the following MCP tools:

- `create_entities`: Create new entities with observations and optional embeddings
- `search_nodes`: Search for entities and their relations using text or vector similarity
- `read_graph`: Get recent entities and their relations
- `create_relations`: Create relations between entities
- `delete_entity`: Delete an entity and all its associated data
- `delete_relation`: Delete a specific relation between entities

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

## Architecture

The project follows a clean, modular architecture:

- `main.go`: Application entry point
- `internal/apptype/`: Core data structures and MCP type definitions
- `internal/database/`: Database client and logic using libSQL
- `internal/server/`: MCP server implementation

## License

MIT
