export interface Entity {
	name: string;
	entityType: string;
	observations: string[];
	embedding?: number[];
}

export interface Relation {
	from: string;
	to: string;
	relationType: string;
	embedding?: number[];
}

export interface SearchResult {
	entity: Entity;
	distance: number;
}
