package apptype

// ProjectArgs provides a standard way to pass project context to tools.
type ProjectArgs struct {
	ProjectName string `json:"projectName,omitempty" jsonschema:"The name of the project to operate on. If not provided, the default project is used."`
}

// CreateEntitiesArgs represents the arguments for the create_entities tool
type CreateEntitiesArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Entities    []Entity    `json:"entities" jsonschema:"A list of entities to create."`
}

// SearchNodesArgs represents the arguments for the search_nodes tool
type SearchNodesArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Query       interface{} `json:"query" jsonschema:"The search query. Can be a string for text search or a []float32 for vector similarity search."`
	Limit       int         `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default 5)."`
	Offset      int         `json:"offset,omitempty" jsonschema:"Number of results to skip (for pagination)."`
}

// CreateRelationsArgs represents the arguments for the create_relations tool
type CreateRelationsArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Relations   []Relation  `json:"relations" jsonschema:"A list of relations to create between entities."`
}

// DeleteEntityArgs represents the arguments for the delete_entity tool
type DeleteEntityArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Name        string      `json:"name" jsonschema:"The name of the entity to delete."`
}

// DeleteRelationArgs represents the arguments for the delete_relation tool
type DeleteRelationArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Source      string      `json:"source" jsonschema:"The name of the source entity in the relation."`
	Target      string      `json:"target" jsonschema:"The name of the target entity in the relation."`
	Type        string      `json:"type" jsonschema:"The type of the relation."`
}

// Bulk deletion/update argument types (planned tools)
type DeleteEntitiesArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty"`
	Names       []string    `json:"names"`
}

type RelationTuple struct {
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relationType"`
}

type DeleteRelationsArgs struct {
	ProjectArgs ProjectArgs     `json:"projectArgs,omitempty"`
	Relations   []RelationTuple `json:"relations"`
}

type DeleteObservationsArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty"`
	EntityName  string      `json:"entityName"`
	IDs         []int64     `json:"ids,omitempty"`
	Contents    []string    `json:"contents,omitempty"`
}

// ReadGraphArgs represents the arguments for the read_graph tool
type ReadGraphArgs struct {
	ProjectArgs ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Limit       int         `json:"limit,omitempty" jsonschema:"Maximum number of recent entities to return (default 10)."`
}

// GraphResult represents the result for graph-related tools (search_nodes, read_graph)
type GraphResult struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}

// AddObservationsArgs represents arguments for appending observations to an entity
type AddObservationsArgs struct {
	ProjectArgs  ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	EntityName   string      `json:"entityName" jsonschema:"The name of the entity to add observations to."`
	Observations []string    `json:"observations" jsonschema:"A list of observations to append."`
}

// OpenNodesArgs represents arguments for fetching entities (and optional relations)
type OpenNodesArgs struct {
	ProjectArgs      ProjectArgs `json:"projectArgs,omitempty" jsonschema:"Project context for the operation."`
	Names            []string    `json:"names" jsonschema:"Entity names to open."`
	IncludeRelations bool        `json:"includeRelations,omitempty" jsonschema:"Whether to include relations among the returned entities."`
}
