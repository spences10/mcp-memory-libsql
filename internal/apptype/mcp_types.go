package apptype

// ProjectArgs provides a standard way to pass project context to tools.
type ProjectArgs struct {
	ProjectName string `json:"projectName,omitempty" jsonschema:"The name of the project to operate on. If not provided, the default project is used."`
}

// CreateEntitiesArgs represents the arguments for the create_entities tool
type CreateEntitiesArgs struct {
	ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Entities    []Entity `json:"entities" jsonschema:"A list of entities to create."`
}

// SearchNodesArgs represents the arguments for the search_nodes tool
type SearchNodesArgs struct {
	ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Query       interface{} `json:"query" jsonschema:"The search query. Can be a string for text search or a []float32 for vector similarity search."`
}

// CreateRelationsArgs represents the arguments for the create_relations tool
type CreateRelationsArgs struct {
	ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Relations   []Relation `json:"relations" jsonschema:"A list of relations to create between entities."`
}

// DeleteEntityArgs represents the arguments for the delete_entity tool
type DeleteEntityArgs struct {
	ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Name        string `json:"name" jsonschema:"The name of the entity to delete."`
}

// DeleteRelationArgs represents the arguments for the delete_relation tool
type DeleteRelationArgs struct {
	ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Source      string `json:"source" jsonschema:"The name of the source entity in the relation."`
	Target      string `json:"target" jsonschema:"The name of the target entity in the relation."`
	Type        string `json:"type" jsonschema:"The type of the relation."`
}

// ReadGraphArgs represents the arguments for the read_graph tool
type ReadGraphArgs struct {
	ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
}

// GraphResult represents the result for graph-related tools (search_nodes, read_graph)
type GraphResult struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}
