package apptype

// CreateEntitiesArgs represents the arguments for the create_entities tool
type CreateEntitiesArgs struct {
	Entities []Entity `json:"entities"`
}

// SearchNodesArgs represents the arguments for the search_nodes tool
type SearchNodesArgs struct {
	Query interface{} `json:"query"` // Can be string or []float32
}

// CreateRelationsArgs represents the arguments for the create_relations tool
type CreateRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

// DeleteEntityArgs represents the arguments for the delete_entity tool
type DeleteEntityArgs struct {
	Name string `json:"name"`
}

// DeleteRelationArgs represents the arguments for the delete_relation tool
type DeleteRelationArgs struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// GraphResult represents the result for graph-related tools (search_nodes, read_graph)
type GraphResult struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}
