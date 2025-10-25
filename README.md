# mcp-memory-libsql

A high-performance, persistent memory system for the Model Context
Protocol (MCP) powered by libSQL with optimized text search for LLM
context efficiency.

<a href="https://glama.ai/mcp/servers/22lg4lq768">
  <img width="380" height="200" src="https://glama.ai/mcp/servers/22lg4lq768/badge" alt="Glama badge" />
</a>

## Features

- 🚀 High-performance text search with relevance ranking
- 💾 Persistent storage of entities and relations
- 🔍 Flexible text search with fuzzy matching
- 🎯 Context-optimized for LLM efficiency
- 🔄 Knowledge graph management
- 🌐 Compatible with local and remote libSQL databases
- 🔒 Secure token-based authentication for remote databases

## Configuration

This server is designed to be used as part of an MCP configuration.
Here are examples for different environments:

### Cline Configuration

Add this to your Cline MCP settings:

```json
{
	"mcpServers": {
		"mcp-memory-libsql": {
			"command": "npx",
			"args": ["-y", "mcp-memory-libsql"],
			"env": {
				"LIBSQL_URL": "file:/path/to/your/database.db"
			}
		}
	}
}
```

### Claude Desktop with WSL Configuration

For a detailed guide on setting up this server with Claude Desktop in
WSL, see
[Getting MCP Server Working with Claude Desktop in WSL](https://scottspence.com/posts/getting-mcp-server-working-with-claude-desktop-in-wsl).

Add this to your Claude Desktop configuration for WSL environments:

```json
{
	"mcpServers": {
		"mcp-memory-libsql": {
			"command": "wsl.exe",
			"args": [
				"bash",
				"-c",
				"source ~/.nvm/nvm.sh && LIBSQL_URL=file:/path/to/database.db /home/username/.nvm/versions/node/v20.12.1/bin/npx mcp-memory-libsql"
			]
		}
	}
}
```

### Database Configuration

The server supports both local SQLite and remote libSQL databases
through the LIBSQL_URL environment variable:

For local SQLite databases:

```json
{
	"env": {
		"LIBSQL_URL": "file:/path/to/database.db"
	}
}
```

For remote libSQL databases (e.g., Turso):

```json
{
	"env": {
		"LIBSQL_URL": "libsql://your-database.turso.io",
		"LIBSQL_AUTH_TOKEN": "your-auth-token"
	}
}
```

Note: When using WSL, ensure the database path uses the Linux
filesystem format (e.g., `/home/username/...`) rather than Windows
format.

By default, if no URL is provided, it will use `file:/memory-tool.db`
in the current directory.

## API

The server implements the standard MCP memory interface with optimized
text search:

- Entity Management
  - Create/Update entities with observations
  - Delete entities
  - Search entities by text with relevance ranking
  - Explore entity relationships
- Relation Management
  - Create relations between entities
  - Delete relations
  - Query related entities

## Architecture

The server uses a libSQL database with the following schema:

- Entities table: Stores entity information with timestamps
- Observations table: Stores entity observations
- Relations table: Stores relationships between entities
- Text search with relevance ranking (name > type > observation)

## Development

### Publishing

Due to npm 2FA requirements, publishing needs to be done manually:

1. Create a changeset (documents your changes):

```bash
pnpm changeset
```

2. Version the package (updates version and CHANGELOG):

```bash
pnpm changeset version
```

3. Publish to npm (will prompt for 2FA code):

```bash
pnpm release
```

## Contributing

Contributions are welcome! Please read our contributing guidelines
before submitting pull requests.

## License

MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Built on the
  [Model Context Protocol](https://github.com/modelcontextprotocol)
- Powered by [libSQL](https://github.com/tursodatabase/libsql)
