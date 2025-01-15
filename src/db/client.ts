import { createClient } from '@libsql/client';
import { Entity, Relation, SearchResult } from '../types/index.js';

// Types for configuration
interface DatabaseConfig {
	url: string;
	authToken?: string;
}

export class DatabaseManager {
	private static instance: DatabaseManager;
	private client;

	private constructor(config: DatabaseConfig) {
		if (!config.url) {
			throw new Error('Database URL is required');
		}
		this.client = createClient({
			url: config.url,
			authToken: config.authToken,
		});
	}

	public static async get_instance(
		config: DatabaseConfig,
	): Promise<DatabaseManager> {
		if (!DatabaseManager.instance) {
			DatabaseManager.instance = new DatabaseManager(config);
			await DatabaseManager.instance.initialize();
		}
		return DatabaseManager.instance;
	}

	// Convert array to vector string representation with validation
	private array_to_vector_string(
		numbers: number[] | undefined,
	): string {
		// If no embedding provided, create a default zero vector
		if (!numbers || !Array.isArray(numbers)) {
			return '[0.0, 0.0, 0.0, 0.0]';
		}

		// Validate vector dimensions match schema (4 dimensions for testing)
		if (numbers.length !== 4) {
			throw new Error(
				`Vector must have exactly 4 dimensions, got ${numbers.length}. Please provide a 4D vector or omit for default zero vector.`,
			);
		}

		// Validate all elements are numbers and convert NaN/Infinity to 0
		const sanitized_numbers = numbers.map((n) => {
			if (typeof n !== 'number' || isNaN(n) || !isFinite(n)) {
				console.warn(
					`Invalid vector value detected, using 0.0 instead of: ${n}`,
				);
				return 0.0;
			}
			return n;
		});

		return `[${sanitized_numbers.join(', ')}]`;
	}

	// Extract vector from binary format
	private async extract_vector(
		embedding: Uint8Array,
	): Promise<number[]> {
		const result = await this.client.execute({
			sql: 'SELECT vector_extract(?) as vec',
			args: [embedding],
		});
		const vecStr = result.rows[0].vec as string;
		return JSON.parse(vecStr);
	}

	// Entity operations
	async create_entities(
		entities: Array<{
			name: string;
			entityType: string;
			observations: string[];
			embedding?: number[];
		}>,
	): Promise<void> {
		try {
			for (const entity of entities) {
				// Validate entity name
				if (
					!entity.name ||
					typeof entity.name !== 'string' ||
					entity.name.trim() === ''
				) {
					throw new Error('Entity name must be a non-empty string');
				}

				// Validate entity type
				if (
					!entity.entityType ||
					typeof entity.entityType !== 'string' ||
					entity.entityType.trim() === ''
				) {
					throw new Error(
						`Invalid entity type for entity "${entity.name}"`,
					);
				}

				// Validate observations
				if (
					!Array.isArray(entity.observations) ||
					entity.observations.length === 0
				) {
					throw new Error(
						`Entity "${entity.name}" must have at least one observation`,
					);
				}

				if (
					!entity.observations.every(
						(obs) => typeof obs === 'string' && obs.trim() !== '',
					)
				) {
					throw new Error(
						`Entity "${entity.name}" has invalid observations. All observations must be non-empty strings`,
					);
				}

				await this.client.execute('BEGIN TRANSACTION');

				try {
					// Check if entity exists
					const existing = await this.client.execute({
						sql: 'SELECT name, entity_type FROM entities WHERE name = ?',
						args: [entity.name],
					});

					const vector_string = this.array_to_vector_string(
						entity.embedding,
					);

					if (existing.rows.length > 0) {
						// Update existing entity
						await this.client.execute({
							sql: 'UPDATE entities SET entity_type = ?, embedding = vector32(?) WHERE name = ?',
							args: [entity.entityType, vector_string, entity.name],
						});
					} else {
						// Insert new entity
						await this.client.execute({
							sql: 'INSERT INTO entities (name, entity_type, embedding) VALUES (?, ?, vector32(?))',
							args: [entity.name, entity.entityType, vector_string],
						});
					}

					// Add new observations
					for (const observation of entity.observations) {
						await this.client.execute({
							sql: 'INSERT INTO observations (entity_name, content) VALUES (?, ?)',
							args: [entity.name, observation],
						});
					}

					await this.client.execute('COMMIT');
				} catch (error) {
					await this.client.execute('ROLLBACK');
					throw new Error(
						`Failed to create/update entity "${entity.name}": ${
							error instanceof Error ? error.message : String(error)
						}`,
					);
				}
			}
		} catch (error) {
			// Wrap all errors with context
			throw new Error(
				`Entity operation failed: ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
	}

	async search_similar(
		embedding: number[],
		limit: number = 5,
	): Promise<SearchResult[]> {
		try {
			// Validate input vector
			if (!Array.isArray(embedding)) {
				throw new Error('Search embedding must be an array');
			}

			const vector_string = this.array_to_vector_string(embedding);

			// Use vector_top_k to find similar entities, excluding zero vectors
			const results = await this.client.execute({
				sql: `
					SELECT e.name, e.entity_type, e.embedding,
						   vector_distance_cos(e.embedding, vector32(?)) as distance
					FROM entities e
					WHERE e.embedding IS NOT NULL
					AND e.embedding != vector32('[0.0, 0.0, 0.0, 0.0]')
					ORDER BY distance ASC
					LIMIT ?
				`,
				args: [vector_string, limit],
			});

			// Get observations for each entity
			const search_results: SearchResult[] = [];
			for (const row of results.rows) {
				try {
					const observations = await this.client.execute({
						sql: 'SELECT content FROM observations WHERE entity_name = ?',
						args: [row.name],
					});

					const entity_embedding = await this.extract_vector(
						row.embedding as Uint8Array,
					);

					search_results.push({
						entity: {
							name: row.name as string,
							entityType: row.entity_type as string,
							observations: observations.rows.map(
								(obs) => obs.content as string,
							),
							embedding: entity_embedding,
						},
						distance: row.distance as number,
					});
				} catch (error) {
					console.warn(
						`Failed to process search result for entity "${
							row.name
						}": ${
							error instanceof Error ? error.message : String(error)
						}`,
					);
					// Continue processing other results
					continue;
				}
			}

			return search_results;
		} catch (error) {
			throw new Error(
				`Similarity search failed: ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
	}

	async get_entity(name: string): Promise<Entity> {
		const entity_result = await this.client.execute({
			sql: 'SELECT name, entity_type, embedding FROM entities WHERE name = ?',
			args: [name],
		});

		if (entity_result.rows.length === 0) {
			throw new Error(`Entity not found: ${name}`);
		}

		const observations_result = await this.client.execute({
			sql: 'SELECT content FROM observations WHERE entity_name = ?',
			args: [name],
		});

		const embedding = entity_result.rows[0].embedding
			? await this.extract_vector(
					entity_result.rows[0].embedding as Uint8Array,
			  )
			: undefined;

		return {
			name: entity_result.rows[0].name as string,
			entityType: entity_result.rows[0].entity_type as string,
			observations: observations_result.rows.map(
				(row) => row.content as string,
			),
			embedding,
		};
	}

	async search_entities(query: string): Promise<Entity[]> {
		const results = await this.client.execute({
			sql: `
        SELECT DISTINCT e.name, e.entity_type, e.embedding
        FROM entities e
        LEFT JOIN observations o ON e.name = o.entity_name
        WHERE e.name LIKE ? OR e.entity_type LIKE ? OR o.content LIKE ?
      `,
			args: [`%${query}%`, `%${query}%`, `%${query}%`],
		});

		const entities: Entity[] = [];
		for (const row of results.rows) {
			const name = row.name as string;
			const observations = await this.client.execute({
				sql: 'SELECT content FROM observations WHERE entity_name = ?',
				args: [name],
			});

			const embedding = row.embedding
				? await this.extract_vector(row.embedding as Uint8Array)
				: undefined;

			entities.push({
				name,
				entityType: row.entity_type as string,
				observations: observations.rows.map(
					(obs) => obs.content as string,
				),
				embedding,
			});
		}

		return entities;
	}

	async get_recent_entities(limit = 10): Promise<Entity[]> {
		const results = await this.client.execute({
			sql: 'SELECT name, entity_type, embedding FROM entities ORDER BY created_at DESC LIMIT ?',
			args: [limit],
		});

		const entities: Entity[] = [];
		for (const row of results.rows) {
			const name = row.name as string;
			const observations = await this.client.execute({
				sql: 'SELECT content FROM observations WHERE entity_name = ?',
				args: [name],
			});

			const embedding = row.embedding
				? await this.extract_vector(row.embedding as Uint8Array)
				: undefined;

			entities.push({
				name,
				entityType: row.entity_type as string,
				observations: observations.rows.map(
					(obs) => obs.content as string,
				),
				embedding,
			});
		}

		return entities;
	}

	// Relation operations
	async create_relations(relations: Relation[]): Promise<void> {
		for (const relation of relations) {
			await this.client.execute({
				sql: 'INSERT INTO relations (source, target, relation_type) VALUES (?, ?, ?)',
				args: [relation.from, relation.to, relation.relationType],
			});
		}
	}

	async delete_entity(name: string): Promise<void> {
		try {
			// Start a transaction
			await this.client.execute('BEGIN TRANSACTION');

			try {
				// Delete associated observations first (due to foreign key)
				await this.client.execute({
					sql: 'DELETE FROM observations WHERE entity_name = ?',
					args: [name],
				});

				// Delete associated relations (due to foreign key)
				await this.client.execute({
					sql: 'DELETE FROM relations WHERE source = ? OR target = ?',
					args: [name, name],
				});

				// Delete the entity
				const result = await this.client.execute({
					sql: 'DELETE FROM entities WHERE name = ?',
					args: [name],
				});

				if (result.rowsAffected === 0) {
					throw new Error(`Entity not found: ${name}`);
				}

				await this.client.execute('COMMIT');
			} catch (error) {
				await this.client.execute('ROLLBACK');
				throw error;
			}
		} catch (error) {
			throw new Error(
				`Failed to delete entity "${name}": ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
	}

	async delete_relation(
		source: string,
		target: string,
		type: string,
	): Promise<void> {
		try {
			const result = await this.client.execute({
				sql: 'DELETE FROM relations WHERE source = ? AND target = ? AND relation_type = ?',
				args: [source, target, type],
			});

			if (result.rowsAffected === 0) {
				throw new Error(
					`Relation not found: ${source} -> ${target} (${type})`,
				);
			}
		} catch (error) {
			throw new Error(
				`Failed to delete relation: ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
	}

	async get_relations_for_entities(
		entities: Entity[],
	): Promise<Relation[]> {
		if (entities.length === 0) return [];

		const entity_names = entities.map((e) => e.name);
		const placeholders = entity_names.map(() => '?').join(',');

		const results = await this.client.execute({
			sql: `
        SELECT source as from_entity, target as to_entity, relation_type 
        FROM relations 
        WHERE source IN (${placeholders}) 
        OR target IN (${placeholders})
      `,
			args: [...entity_names, ...entity_names],
		});

		return results.rows.map((row) => ({
			from: row.from_entity as string,
			to: row.to_entity as string,
			relationType: row.relation_type as string,
		}));
	}

	// Graph operations
	async read_graph(): Promise<{
		entities: Entity[];
		relations: Relation[];
	}> {
		const recent_entities = await this.get_recent_entities();
		const relations = await this.get_relations_for_entities(
			recent_entities,
		);
		return { entities: recent_entities, relations };
	}

	async search_nodes(
		query: string | number[],
	): Promise<{ entities: Entity[]; relations: Relation[] }> {
		try {
			let entities: Entity[];

			if (Array.isArray(query)) {
				// Validate vector query
				if (!query.every((n) => typeof n === 'number')) {
					throw new Error('Vector query must contain only numbers');
				}
				// Vector similarity search
				const results = await this.search_similar(query);
				entities = results.map((r) => r.entity);
			} else {
				// Validate text query
				if (typeof query !== 'string') {
					throw new Error('Text query must be a string');
				}
				if (query.trim() === '') {
					throw new Error('Text query cannot be empty');
				}
				// Text-based search
				entities = await this.search_entities(query);
			}

			// If no entities found, return empty result
			if (entities.length === 0) {
				return { entities: [], relations: [] };
			}

			const relations = await this.get_relations_for_entities(
				entities,
			);
			return { entities, relations };
		} catch (error) {
			throw new Error(
				`Node search failed: ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
	}

	// Database operations
	public get_client() {
		return this.client;
	}

	public async initialize() {
		// Create tables if they don't exist
		await this.client.execute(`
      CREATE TABLE IF NOT EXISTS entities (
        name TEXT PRIMARY KEY,
        entity_type TEXT NOT NULL,
        embedding F32_BLOB(4), -- 4-dimension vector for testing
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
      )
    `);

		await this.client.execute(`
      CREATE TABLE IF NOT EXISTS observations (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        entity_name TEXT NOT NULL,
        content TEXT NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (entity_name) REFERENCES entities(name)
      )
    `);

		await this.client.execute(`
      CREATE TABLE IF NOT EXISTS relations (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        source TEXT NOT NULL,
        target TEXT NOT NULL,
        relation_type TEXT NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (source) REFERENCES entities(name),
        FOREIGN KEY (target) REFERENCES entities(name)
      )
    `);

		// Create indexes
		await this.client.execute(`
      CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(name);
      CREATE INDEX IF NOT EXISTS idx_observations_entity ON observations(entity_name);
      CREATE INDEX IF NOT EXISTS idx_relations_source ON relations(source);
      CREATE INDEX IF NOT EXISTS idx_relations_target ON relations(target);
      CREATE INDEX IF NOT EXISTS idx_entities_embedding ON entities(libsql_vector_idx(embedding));
    `);
	}

	public async close() {
		try {
			await this.client.close();
		} catch (error) {
			console.error('Error closing database connection:', error);
		}
	}
}

export type { DatabaseConfig };
