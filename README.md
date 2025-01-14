# mcp-memory-libsql

A high-performance, persistent memory system for the Model Context Protocol (MCP) powered by libSQL. This server provides vector search capabilities and efficient knowledge storage using libSQL as the backing store.

## Features

- 🚀 High-performance vector search using libSQL
- 💾 Persistent storage of entities and relations
- 🔍 Semantic search capabilities
- 🔄 Knowledge graph management
- 🌐 Compatible with local and remote libSQL databases
- 🔒 Secure token-based authentication for remote databases

## Installation

```bash
npm install mcp-memory-libsql
```

Or with pnpm:

```bash
pnpm add mcp-memory-libsql
```

## Usage

### Starting the Server

You can start the server using npx:

```bash
npx mcp-memory-libsql
```

### Configuration

The server can be configured by passing environment variables when starting the server:

```bash
# For a local SQLite database:
LIBSQL_URL=file:/path/to/your/database.db npx mcp-memory-libsql

# For a remote libSQL database (e.g., Turso):
LIBSQL_URL=libsql://your-database.turso.io LIBSQL_AUTH_TOKEN=your-auth-token npx mcp-memory-libsql
```

By default, if no URL is provided, it will use `file:/memory-tool.db` in the current directory.

### Claude Desktop Configuration

Add this to your Claude Desktop configuration:

```json
{
	"mcpServers": {
		"memory": {
			"command": "npx",
			"args": ["-y", "mcp-memory-libsql"]
		}
	}
}
```

## Development

### Prerequisites

- Node.js 22.13.0 or higher
- pnpm (recommended) or npm

### Setup

1. Clone the repository:

```bash
git clone https://github.com/yourusername/mcp-memory-libsql.git
cd mcp-memory-libsql
```

2. Install dependencies:

```bash
pnpm install
```

3. Run database migrations:

```bash
pnpm run migrate
```

4. Build the project:

```bash
pnpm run build
```

### Running Tests

```bash
pnpm test
```

### Development Mode

```bash
pnpm run dev
```

## API

The server implements the standard MCP memory interface with additional vector search capabilities:

- Entity Management
  - Create/Update entities with embeddings
  - Delete entities
  - Search entities by similarity
- Relation Management
  - Create relations between entities
  - Delete relations
  - Query related entities

## Architecture

The server uses a libSQL database with the following schema:

- Entities table: Stores entity information and embeddings
- Relations table: Stores relationships between entities
- Vector search capabilities implemented using libSQL's built-in vector operations

## Contributing

Contributions are welcome! Please read our contributing guidelines before submitting pull requests.

## License

MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Built on the [Model Context Protocol](https://github.com/modelcontextprotocol)
- Powered by [libSQL](https://github.com/tursodatabase/libsql)
