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
- **Multi-Project Support**: Optionally, run in a mode that manages separate databases for multiple projects.

## Installation

To install the `mcp-memory-libsql-go` binary to a standard location on your system, use the following command:

```bash
make install
```

This will compile the binary and install it in a standard directory (e.g., `~/.local/bin` on Linux or `/usr/local/bin` on macOS), which should be in your system's `PATH`.

## Usage

### Command-line Flags

- `-libsql-url`: Database URL (default: `file:./memory-tool.db`). Overrides the `LIBSQL_URL` environment variable.
- `-auth-token`: Authentication token for remote databases. Overrides the `LIBSQL_AUTH_TOKEN` environment variable.
- `-projects-dir`: Base directory for projects. Enables multi-project mode. If this is set, `-libsql-url` is ignored.

### Environment Variables

- `LIBSQL_URL`: Database URL (default: `file:./memory-tool.db`)
  - Local file: `file:./path/to/db.sqlite`
  - Remote libSQL: `libsql://your-db.turso.io`
- `LIBSQL_AUTH_TOKEN`: Authentication token for remote databases

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

### Using Tools in Multi-Project Mode

When in multi-project mode, all tools accept an optional `projectName` field in their arguments. If `projectName` is not provided, it will use the "default" project.

**Example `create_entities` call:**

```json
{
  "tool_name": "create_entities",
  "arguments": {
    "projectName": "my-awesome-project",
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
      "command": "/path/to/your/mcp-memory-libsql-go",
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
      "command": "/path/to/your/mcp-memory-libsql-go",
      "args": ["-projects-dir", "/path/to/your/projects"]
    }
  }
}
```

Remember to replace `/path/to/your/mcp-memory-libsql-go` with the actual path to the compiled binary.

## Architecture

The project follows a clean, modular architecture:

- `main.go`: Application entry point
- `internal/apptype/`: Core data structures and MCP type definitions
- `internal/database/`: Database client and logic using libSQL
- `internal/server/`: MCP server implementation

## License

MIT
