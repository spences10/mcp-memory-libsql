#!/usr/bin/env node
import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import {
	CallToolRequest,
	CallToolRequestSchema,
	ErrorCode,
	ListToolsRequestSchema,
	McpError,
} from '@modelcontextprotocol/sdk/types.js';
import { readFileSync } from 'fs';
import { dirname, join } from 'path';
import { fileURLToPath } from 'url';
import { DatabaseManager } from './db/client.js';
import { get_database_config } from './db/config.js';
import { Relation } from './types/index.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const pkg = JSON.parse(
	readFileSync(join(__dirname, '..', 'package.json'), 'utf8'),
);
const { name, version } = pkg;

class LibSqlMemoryServer {
	private server: Server;
	private db!: DatabaseManager;

	private constructor() {
		this.server = new Server(
			{ name, version },
			{
				capabilities: {
					tools: {
						create_entities: {},
						search_nodes: {},
						read_graph: {},
						create_relations: {},
					},
				},
			},
		);

		// Error handling
		this.server.onerror = (error: Error) =>
			console.error('[MCP Error]', error);
		process.on('SIGINT', async () => {
			await this.db?.close();
			await this.server.close();
			process.exit(0);
		});
	}

	public static async create(): Promise<LibSqlMemoryServer> {
		const instance = new LibSqlMemoryServer();
		const config = get_database_config();
		instance.db = await DatabaseManager.get_instance(config);
		instance.setup_tool_handlers();
		return instance;
	}

	private setup_tool_handlers() {
		this.server.setRequestHandler(
			ListToolsRequestSchema,
			async () => ({
				tools: [
					{
						name: 'create_entities',
						description:
							'Create new entities with observations and optional embeddings',
						inputSchema: {
							type: 'object',
							properties: {
								entities: {
									type: 'array',
									items: {
										type: 'object',
										properties: {
											name: { type: 'string' },
											entityType: { type: 'string' },
											observations: {
												type: 'array',
												items: { type: 'string' },
											},
											embedding: {
												type: 'array',
												items: { type: 'number' },
												description:
													'Optional vector embedding for similarity search',
											},
										},
										required: ['name', 'entityType', 'observations'],
									},
								},
							},
							required: ['entities'],
						},
					},
					{
						name: 'search_nodes',
						description:
							'Search for entities and their relations using text or vector similarity',
						inputSchema: {
							type: 'object',
							properties: {
								query: {
									oneOf: [
										{
											type: 'string',
											description: 'Text search query',
										},
										{
											type: 'array',
											items: { type: 'number' },
											description: 'Vector for similarity search',
										},
									],
								},
							},
							required: ['query'],
						},
					},
					{
						name: 'read_graph',
						description: 'Get recent entities and their relations',
						inputSchema: {
							type: 'object',
							properties: {},
							required: [],
						},
					},
					{
						name: 'create_relations',
						description: 'Create relations between entities',
						inputSchema: {
							type: 'object',
							properties: {
								relations: {
									type: 'array',
									items: {
										type: 'object',
										properties: {
											source: { type: 'string' },
											target: { type: 'string' },
											type: { type: 'string' },
										},
										required: ['source', 'target', 'type'],
									},
								},
							},
							required: ['relations'],
						},
					},
				],
			}),
		);

		this.server.setRequestHandler(
			CallToolRequestSchema,
			async (request: CallToolRequest) => {
				try {
					switch (request.params.name) {
						case 'create_entities': {
							const entities = request.params.arguments
								?.entities as Array<{
								name: string;
								entityType: string;
								observations: string[];
								embedding?: number[];
							}>;
							if (!entities) {
								throw new McpError(
									ErrorCode.InvalidParams,
									'Missing entities parameter',
								);
							}
							await this.db.create_entities(entities);
							return {
								content: [
									{
										type: 'text',
										text: `Created ${entities.length} entities`,
									},
								],
							};
						}

						case 'search_nodes': {
							const query = request.params.arguments?.query;
							if (query === undefined || query === null) {
								throw new McpError(
									ErrorCode.InvalidParams,
									'Missing query parameter',
								);
							}
							// Validate query type
							if (
								!(typeof query === 'string' || Array.isArray(query))
							) {
								throw new McpError(
									ErrorCode.InvalidParams,
									'Query must be either a string or number array',
								);
							}
							const result = await this.db.search_nodes(query);
							return {
								content: [
									{
										type: 'text',
										text: JSON.stringify(result, null, 2),
									},
								],
							};
						}

						case 'read_graph': {
							const result = await this.db.read_graph();
							return {
								content: [
									{
										type: 'text',
										text: JSON.stringify(result, null, 2),
									},
								],
							};
						}

						case 'create_relations': {
							const relations = request.params.arguments
								?.relations as Array<{
								source: string;
								target: string;
								type: string;
							}>;
							if (!relations) {
								throw new McpError(
									ErrorCode.InvalidParams,
									'Missing relations parameter',
								);
							}
							// Convert to internal Relation type
							const internalRelations: Relation[] = relations.map(
								(r) => ({
									from: r.source,
									to: r.target,
									relationType: r.type,
								}),
							);
							await this.db.create_relations(internalRelations);
							return {
								content: [
									{
										type: 'text',
										text: `Created ${relations.length} relations`,
									},
								],
							};
						}

						default:
							throw new McpError(
								ErrorCode.MethodNotFound,
								`Unknown tool: ${request.params.name}`,
							);
					}
				} catch (error) {
					if (error instanceof McpError) throw error;
					throw new McpError(
						ErrorCode.InternalError,
						error instanceof Error ? error.message : String(error),
					);
				}
			},
		);
	}

	async run() {
		const transport = new StdioServerTransport();
		await this.server.connect(transport);
		console.error('LibSQL Memory MCP server running on stdio');
	}
}

LibSqlMemoryServer.create()
	.then((server) => server.run())
	.catch(console.error);
