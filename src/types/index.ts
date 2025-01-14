export interface Entity {
  name: string;
  entityType: string;
  observations: string[];
  embedding?: number[]; // 384-dimension vector
}

export interface Relation {
  from: string;
  to: string;
  relationType: string;
  embedding?: number[]; // 384-dimension vector
}

export interface SearchResult {
  entity: Entity;
  distance: number;
}
