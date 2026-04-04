import { existsSync, unlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { DatabaseManager } from './client.js';

describe('DatabaseManager', () => {
	let db: DatabaseManager;
	let db_path: string;

	beforeEach(async () => {
		// Reset singleton so get_instance creates fresh
		// @ts-expect-error accessing private static for test reset
		DatabaseManager.instance = undefined;

		db_path = join(
			tmpdir(),
			`test-${Date.now()}-${Math.random().toString(36).slice(2)}.db`,
		);
		db = await DatabaseManager.get_instance({
			url: `file:${db_path}`,
		});
	});

	afterEach(async () => {
		await db?.close();
		// Clean up temp DB file
		if (existsSync(db_path)) unlinkSync(db_path);
	});

	describe('create_entities', () => {
		it('creates an entity with observations', async () => {
			await db.create_entities([
				{
					name: 'test-entity',
					entityType: 'person',
					observations: ['likes coffee'],
				},
			]);
			const entity = await db.get_entity('test-entity');
			expect(entity.name).toBe('test-entity');
			expect(entity.entityType).toBe('person');
			expect(entity.observations).toEqual(['likes coffee']);
		});

		it('updates existing entity on duplicate name', async () => {
			await db.create_entities([
				{
					name: 'test',
					entityType: 'v1',
					observations: ['old'],
				},
			]);
			await db.create_entities([
				{
					name: 'test',
					entityType: 'v2',
					observations: ['new'],
				},
			]);
			const entity = await db.get_entity('test');
			expect(entity.entityType).toBe('v2');
			expect(entity.observations).toEqual(['new']);
		});

		it('rejects empty entity name', async () => {
			await expect(
				db.create_entities([
					{
						name: '',
						entityType: 'type',
						observations: ['obs'],
					},
				]),
			).rejects.toThrow('non-empty string');
		});

		it('rejects empty observations', async () => {
			await expect(
				db.create_entities([
					{
						name: 'test',
						entityType: 'type',
						observations: [],
					},
				]),
			).rejects.toThrow('at least one observation');
		});

		it('rejects too many observations', async () => {
			const observations = Array.from(
				{ length: 101 },
				(_, i) => `obs ${i}`,
			);
			await expect(
				db.create_entities([
					{
						name: 'test',
						entityType: 'type',
						observations,
					},
				]),
			).rejects.toThrow('exceeds maximum');
		});

		it('sanitizes control characters from observations', async () => {
			await db.create_entities([
				{
					name: 'test',
					entityType: 'type',
					observations: ['hello\x00world'],
				},
			]);
			const entity = await db.get_entity('test');
			expect(entity.observations[0]).toBe('helloworld');
		});

		it('sanitizes entity name', async () => {
			await db.create_entities([
				{
					name: 'test\x00name',
					entityType: 'type',
					observations: ['obs'],
				},
			]);
			const entity = await db.get_entity('testname');
			expect(entity.name).toBe('testname');
		});

		it('truncates long entity names', async () => {
			const long_name = 'a'.repeat(300);
			await db.create_entities([
				{
					name: long_name,
					entityType: 'type',
					observations: ['obs'],
				},
			]);
			const entity = await db.get_entity('a'.repeat(256));
			expect(entity.name).toBe('a'.repeat(256));
		});

		it('truncates long observations', async () => {
			const long_obs = 'x'.repeat(5000);
			await db.create_entities([
				{
					name: 'test',
					entityType: 'type',
					observations: [long_obs],
				},
			]);
			const entity = await db.get_entity('test');
			expect(entity.observations[0].length).toBe(4096);
		});
	});

	describe('search_nodes', () => {
		beforeEach(async () => {
			await db.create_entities([
				{
					name: 'Alice',
					entityType: 'person',
					observations: ['software engineer'],
				},
				{
					name: 'Bob',
					entityType: 'person',
					observations: ['product manager'],
				},
				{
					name: 'Acme Corp',
					entityType: 'company',
					observations: ['tech startup'],
				},
			]);
		});

		it('finds entities by name', async () => {
			const result = await db.search_nodes('Alice');
			expect(result.entities).toHaveLength(1);
			expect(result.entities[0].name).toBe('Alice');
		});

		it('finds entities by observation content', async () => {
			const result = await db.search_nodes('engineer');
			expect(result.entities).toHaveLength(1);
			expect(result.entities[0].name).toBe('Alice');
		});

		it('finds entities by type', async () => {
			const result = await db.search_nodes('company');
			expect(result.entities).toHaveLength(1);
			expect(result.entities[0].name).toBe('Acme Corp');
		});

		it('returns empty for no matches', async () => {
			const result = await db.search_nodes('nonexistent');
			expect(result.entities).toHaveLength(0);
			expect(result.relations).toHaveLength(0);
		});

		it('rejects empty query', async () => {
			await expect(db.search_nodes('')).rejects.toThrow(
				'cannot be empty',
			);
		});

		it('respects limit parameter', async () => {
			const result = await db.search_nodes('person', 1);
			expect(result.entities).toHaveLength(1);
		});
	});

	describe('relations', () => {
		beforeEach(async () => {
			await db.create_entities([
				{
					name: 'Alice',
					entityType: 'person',
					observations: ['engineer'],
				},
				{
					name: 'Bob',
					entityType: 'person',
					observations: ['manager'],
				},
			]);
		});

		it('creates and retrieves relations', async () => {
			await db.create_relations([
				{ from: 'Alice', to: 'Bob', relationType: 'works_with' },
			]);
			const graph = await db.read_graph();
			expect(graph.relations).toHaveLength(1);
			expect(graph.relations[0].from).toBe('Alice');
			expect(graph.relations[0].to).toBe('Bob');
		});

		it('deletes a specific relation', async () => {
			await db.create_relations([
				{ from: 'Alice', to: 'Bob', relationType: 'works_with' },
			]);
			await db.delete_relation('Alice', 'Bob', 'works_with');
			const graph = await db.read_graph();
			expect(graph.relations).toHaveLength(0);
		});

		it('throws when deleting nonexistent relation', async () => {
			await expect(
				db.delete_relation('Alice', 'Bob', 'nope'),
			).rejects.toThrow('not found');
		});
	});

	describe('delete_entity', () => {
		it('deletes entity and cascades', async () => {
			await db.create_entities([
				{
					name: 'Alice',
					entityType: 'person',
					observations: ['engineer'],
				},
				{
					name: 'Bob',
					entityType: 'person',
					observations: ['manager'],
				},
			]);
			await db.create_relations([
				{ from: 'Alice', to: 'Bob', relationType: 'knows' },
			]);

			await db.delete_entity('Alice');

			await expect(db.get_entity('Alice')).rejects.toThrow(
				'not found',
			);
			const graph = await db.read_graph();
			expect(graph.relations).toHaveLength(0);
			expect(graph.entities).toHaveLength(1);
		});

		it('throws when deleting nonexistent entity', async () => {
			await expect(
				db.delete_entity('nonexistent'),
			).rejects.toThrow('not found');
		});
	});

	describe('read_graph', () => {
		it('returns empty graph when no data', async () => {
			const graph = await db.read_graph();
			expect(graph.entities).toHaveLength(0);
			expect(graph.relations).toHaveLength(0);
		});

		it('returns entities with their relations', async () => {
			await db.create_entities([
				{
					name: 'A',
					entityType: 'node',
					observations: ['first'],
				},
				{
					name: 'B',
					entityType: 'node',
					observations: ['second'],
				},
			]);
			await db.create_relations([
				{ from: 'A', to: 'B', relationType: 'links_to' },
			]);

			const graph = await db.read_graph();
			expect(graph.entities).toHaveLength(2);
			expect(graph.relations).toHaveLength(1);
		});
	});
});
