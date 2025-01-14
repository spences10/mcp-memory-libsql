import { createClient, InValue } from "@libsql/client";
import { Entity, Relation, SearchResult } from "../types/index.js";

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

  public static async getInstance(config: DatabaseConfig): Promise<DatabaseManager> {
    if (!DatabaseManager.instance) {
      DatabaseManager.instance = new DatabaseManager(config);
      await DatabaseManager.instance.initialize();
    }
    return DatabaseManager.instance;
  }

  // Convert array to vector string representation
  private arrayToVectorString(numbers: number[]): string {
    return `[${numbers.join(', ')}]`;
  }

  // Extract vector from binary format
  private async extractVector(embedding: Uint8Array): Promise<number[]> {
    const result = await this.client.execute({
      sql: "SELECT vector_extract(?) as vec",
      args: [embedding]
    });
    const vecStr = result.rows[0].vec as string;
    return JSON.parse(vecStr);
  }

  // Entity operations
  async createEntities(
    entities: Array<{
      name: string;
      entityType: string;
      observations: string[];
      embedding?: number[];
    }>
  ): Promise<void> {
    for (const entity of entities) {
      // Insert entity with vector32 conversion
      await this.client.execute({
        sql: "INSERT INTO entities (name, entity_type, embedding) VALUES (?, ?, vector32(?))",
        args: [
          entity.name,
          entity.entityType,
          entity.embedding ? this.arrayToVectorString(entity.embedding) : null
        ],
      });

      // Insert observations
      for (const observation of entity.observations) {
        await this.client.execute({
          sql: "INSERT INTO observations (entity_name, content) VALUES (?, ?)",
          args: [entity.name, observation],
        });
      }
    }
  }

  async searchSimilar(embedding: number[], limit: number = 5): Promise<SearchResult[]> {
    // Use vector_top_k to find similar entities
    const results = await this.client.execute({
      sql: `
        SELECT e.name, e.entity_type, e.embedding,
               vector_distance_cos(e.embedding, vector32(?)) as distance
        FROM entities e
        WHERE e.embedding IS NOT NULL
        ORDER BY distance ASC
        LIMIT ?
      `,
      args: [
        this.arrayToVectorString(embedding),
        limit
      ],
    });

    // Get observations for each entity
    const searchResults: SearchResult[] = [];
    for (const row of results.rows) {
      const observations = await this.client.execute({
        sql: "SELECT content FROM observations WHERE entity_name = ?",
        args: [row.name],
      });

      const entityEmbedding = await this.extractVector(row.embedding as Uint8Array);

      searchResults.push({
        entity: {
          name: row.name as string,
          entityType: row.entity_type as string,
          observations: observations.rows.map(obs => obs.content as string),
          embedding: entityEmbedding
        },
        distance: row.distance as number
      });
    }

    return searchResults;
  }

  async getEntity(name: string): Promise<Entity> {
    const entityResult = await this.client.execute({
      sql: "SELECT name, entity_type, embedding FROM entities WHERE name = ?",
      args: [name],
    });

    if (entityResult.rows.length === 0) {
      throw new Error(`Entity not found: ${name}`);
    }

    const observationsResult = await this.client.execute({
      sql: "SELECT content FROM observations WHERE entity_name = ?",
      args: [name],
    });

    const embedding = entityResult.rows[0].embedding 
      ? await this.extractVector(entityResult.rows[0].embedding as Uint8Array)
      : undefined;

    return {
      name: entityResult.rows[0].name as string,
      entityType: entityResult.rows[0].entity_type as string,
      observations: observationsResult.rows.map(row => row.content as string),
      embedding
    };
  }

  async searchEntities(query: string): Promise<Entity[]> {
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
        sql: "SELECT content FROM observations WHERE entity_name = ?",
        args: [name],
      });

      const embedding = row.embedding 
        ? await this.extractVector(row.embedding as Uint8Array)
        : undefined;

      entities.push({
        name,
        entityType: row.entity_type as string,
        observations: observations.rows.map(obs => obs.content as string),
        embedding
      });
    }

    return entities;
  }

  async getRecentEntities(limit = 10): Promise<Entity[]> {
    const results = await this.client.execute({
      sql: "SELECT name, entity_type, embedding FROM entities ORDER BY created_at DESC LIMIT ?",
      args: [limit],
    });

    const entities: Entity[] = [];
    for (const row of results.rows) {
      const name = row.name as string;
      const observations = await this.client.execute({
        sql: "SELECT content FROM observations WHERE entity_name = ?",
        args: [name],
      });

      const embedding = row.embedding 
        ? await this.extractVector(row.embedding as Uint8Array)
        : undefined;

      entities.push({
        name,
        entityType: row.entity_type as string,
        observations: observations.rows.map(obs => obs.content as string),
        embedding
      });
    }

    return entities;
  }

  // Relation operations
  async createRelations(relations: Relation[]): Promise<void> {
    for (const relation of relations) {
      await this.client.execute({
        sql: "INSERT INTO relations (source, target, relation_type) VALUES (?, ?, ?)",
        args: [relation.from, relation.to, relation.relationType],
      });
    }
  }

  async getRelationsForEntities(entities: Entity[]): Promise<Relation[]> {
    if (entities.length === 0) return [];

    const entityNames = entities.map(e => e.name);
    const placeholders = entityNames.map(() => "?").join(",");

    const results = await this.client.execute({
      sql: `
        SELECT source as from_entity, target as to_entity, relation_type 
        FROM relations 
        WHERE source IN (${placeholders}) 
        OR target IN (${placeholders})
      `,
      args: [...entityNames, ...entityNames],
    });

    return results.rows.map(row => ({
      from: row.from_entity as string,
      to: row.to_entity as string,
      relationType: row.relation_type as string,
    }));
  }

  // Graph operations
  async readGraph(): Promise<{ entities: Entity[]; relations: Relation[] }> {
    const recentEntities = await this.getRecentEntities();
    const relations = await this.getRelationsForEntities(recentEntities);
    return { entities: recentEntities, relations };
  }

  async searchNodes(query: string | number[]): Promise<{ entities: Entity[]; relations: Relation[] }> {
    let entities: Entity[];
    
    if (Array.isArray(query)) {
      // Vector similarity search
      const results = await this.searchSimilar(query);
      entities = results.map(r => r.entity);
    } else {
      // Text-based search
      entities = await this.searchEntities(query);
    }
    
    const relations = await this.getRelationsForEntities(entities);
    return { entities, relations };
  }

  // Database operations
  public getClient() {
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
      console.error("Error closing database connection:", error);
    }
  }
}

export type { DatabaseConfig };
