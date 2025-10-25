import { createClient } from '@libsql/client';
import { Entity, Relation } from '../types/index.js';

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

	// Entity operations
	async create_entities(
		entities: Array<{
			name: string;
			entityType: string;
			observations: string[];
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

				// Start a transaction
				const txn = await this.client.transaction('write');

				try {
					// First try to update
					const result = await txn.execute({
						sql: 'UPDATE entities SET entity_type = ? WHERE name = ?',
						args: [entity.entityType, entity.name],
					});

					// If no rows affected, do insert
					if (result.rowsAffected === 0) {
						await txn.execute({
							sql: 'INSERT INTO entities (name, entity_type) VALUES (?, ?)',
							args: [entity.name, entity.entityType],
						});
					}

					// Clear old observations
					await txn.execute({
						sql: 'DELETE FROM observations WHERE entity_name = ?',
						args: [entity.name],
					});

					// Add new observations
					for (const observation of entity.observations) {
						await txn.execute({
							sql: 'INSERT INTO observations (entity_name, content) VALUES (?, ?)',
							args: [entity.name, observation],
						});
					}

					await txn.commit();
				} catch (error) {
					await txn.rollback();
					throw error;
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

	async get_entity(name: string): Promise<Entity> {
		const entity_result = await this.client.execute({
			sql: 'SELECT name, entity_type FROM entities WHERE name = ?',
			args: [name],
		});

		if (entity_result.rows.length === 0) {
			throw new Error(`Entity not found: ${name}`);
		}

		const observations_result = await this.client.execute({
			sql: 'SELECT content FROM observations WHERE entity_name = ?',
			args: [name],
		});

		return {
			name: entity_result.rows[0].name as string,
			entityType: entity_result.rows[0].entity_type as string,
			observations: observations_result.rows.map(
				(row) => row.content as string,
			),
		};
	}

	async search_entities(
		query: string,
		limit: number = 10,
	): Promise<Entity[]> {
		// Normalize query for fuzzy matching
		const normalized_query = `%${query.replace(/[\s_-]+/g, '%')}%`;

		const results = await this.client.execute({
			sql: `
        SELECT DISTINCT
          e.name,
          e.entity_type,
          e.created_at,
          CASE
            WHEN e.name LIKE ? COLLATE NOCASE THEN 3
            WHEN e.entity_type LIKE ? COLLATE NOCASE THEN 2
            ELSE 1
          END as relevance_score
        FROM entities e
        LEFT JOIN observations o ON e.name = o.entity_name
        WHERE e.name LIKE ? COLLATE NOCASE
           OR e.entity_type LIKE ? COLLATE NOCASE
           OR o.content LIKE ? COLLATE NOCASE
        ORDER BY relevance_score DESC, e.created_at DESC
        LIMIT ?
      `,
			args: [
				normalized_query,
				normalized_query,
				normalized_query,
				normalized_query,
				normalized_query,
				limit > 50 ? 50 : limit, // Cap at 50
			],
		});

		const entities: Entity[] = [];
		for (const row of results.rows) {
			const name = row.name as string;
			const observations = await this.client.execute({
				sql: 'SELECT content FROM observations WHERE entity_name = ?',
				args: [name],
			});

			entities.push({
				name,
				entityType: row.entity_type as string,
				observations: observations.rows.map(
					(obs) => obs.content as string,
				),
			});
		}

		return entities;
	}

	async get_recent_entities(limit = 10): Promise<Entity[]> {
		// Cap limit at 50 to prevent context overload
		const safe_limit = limit > 50 ? 50 : limit;

		const results = await this.client.execute({
			sql: 'SELECT name, entity_type FROM entities ORDER BY created_at DESC LIMIT ?',
			args: [safe_limit],
		});

		const entities: Entity[] = [];
		for (const row of results.rows) {
			const name = row.name as string;
			const observations = await this.client.execute({
				sql: 'SELECT content FROM observations WHERE entity_name = ?',
				args: [name],
			});

			entities.push({
				name,
				entityType: row.entity_type as string,
				observations: observations.rows.map(
					(obs) => obs.content as string,
				),
			});
		}

		return entities;
	}

	// Relation operations
	async create_relations(relations: Relation[]): Promise<void> {
		try {
			if (relations.length === 0) return;

			// Prepare batch statements for all relations
			const batch_statements = relations.map((relation) => ({
				sql: 'INSERT INTO relations (source, target, relation_type) VALUES (?, ?, ?)',
				args: [relation.from, relation.to, relation.relationType],
			}));

			// Execute all inserts in a single batch transaction
			await this.client.batch(batch_statements, 'write');
		} catch (error) {
			throw new Error(
				`Failed to create relations: ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
	}

	async delete_entity(name: string): Promise<void> {
		try {
			// Check if entity exists first
			const existing = await this.client.execute({
				sql: 'SELECT name FROM entities WHERE name = ?',
				args: [name],
			});

			if (existing.rows.length === 0) {
				throw new Error(`Entity not found: ${name}`);
			}

			// Prepare batch statements for deletion
			const batch_statements = [
				{
					// Delete associated observations first (due to foreign key)
					sql: 'DELETE FROM observations WHERE entity_name = ?',
					args: [name],
				},
				{
					// Delete associated relations (due to foreign key)
					sql: 'DELETE FROM relations WHERE source = ? OR target = ?',
					args: [name, name],
				},
				{
					// Delete the entity
					sql: 'DELETE FROM entities WHERE name = ?',
					args: [name],
				},
			];

			// Execute all deletions in a single batch transaction
			await this.client.batch(batch_statements, 'write');
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
		query: string,
		limit?: number,
	): Promise<{ entities: Entity[]; relations: Relation[] }> {
		try {
			// Validate text query
			if (typeof query !== 'string') {
				throw new Error('Text query must be a string');
			}
			if (query.trim() === '') {
				throw new Error('Text query cannot be empty');
			}

			// Text-based search with optional limit
			const entities = await this.search_entities(query, limit);

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
		try {
			// Create tables if they don't exist - each as a single statement
			await this.client.execute(`
				CREATE TABLE IF NOT EXISTS entities (
					name TEXT PRIMARY KEY,
					entity_type TEXT NOT NULL,
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

			// Create all indexes in a single batch transaction
			await this.client.batch(
				[
					{
						sql: 'CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(name)',
						args: [],
					},
					{
						sql: 'CREATE INDEX IF NOT EXISTS idx_observations_entity ON observations(entity_name)',
						args: [],
					},
					{
						sql: 'CREATE INDEX IF NOT EXISTS idx_relations_source ON relations(source)',
						args: [],
					},
					{
						sql: 'CREATE INDEX IF NOT EXISTS idx_relations_target ON relations(target)',
						args: [],
					},
				],
				'write',
			);
		} catch (error) {
			throw new Error(
				`Database initialization failed: ${
					error instanceof Error ? error.message : String(error)
				}`,
			);
		}
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
