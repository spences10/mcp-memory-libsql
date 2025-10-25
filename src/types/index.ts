export interface Entity {
	name: string;
	entityType: string;
	observations: string[];
}

export interface Relation {
	from: string;
	to: string;
	relationType: string;
}
