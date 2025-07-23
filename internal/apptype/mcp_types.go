package apptype

// ProjectArgs provides a standard way to pass project context to tools.
type ProjectArgs struct {
	ProjectName string `json:"projectName,omitempty" jsonschema:"The name of the project to operate on. If not provided, the default project is used."`
}

// CreateEntitiesArgs represents the arguments for the create_entities tool
type CreateEntitiesArgs struct {
	ProjectArgs
	Entities []Entity `json:"entities" jsonschema:"A list of entities to create."`
}

// SearchNodesArgs represents the arguments for the search_nodes tool
type SearchNodesArgs struct {
	ProjectArgs
	Query interface{} `json:"query" jsonschema:"The search query. Can be a string for text search or a []float32 for vector similarity search."`
}

// CreateRelationsArgs represents the arguments for the create_relations tool
type CreateRelationsArgs struct {
	ProjectArgs
	Relations []Relation `json:"relations" jsonschema:"A list of relations to create between entities."`
}

// DeleteEntityArgs represents the arguments for the delete_entity tool
type DeleteEntityArgs struct {
	ProjectArgs
	Name string `json:"name" jsonschema:"The name of the entity to delete."`
}

// DeleteRelationArgs represents the arguments for the delete_relation tool
type DeleteRelationArgs struct {
	ProjectArgs
	Source string `json:"source" jsonschema:"The name of the source entity in the relation."`
	Target string `json:"target" jsonschema:"The name of the target entity in the relation."`
	Type   string `json:"type" jsonschema:"The type of the relation."`
}

// ReadGraphArgs represents the arguments for the read_graph tool
type ReadGraphArgs struct {
	ProjectArgs
}

// GraphResult represents the result for graph-related tools (search_nodes, read_graph)
type GraphResult struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}
