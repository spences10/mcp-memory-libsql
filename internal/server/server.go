package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/buildinfo"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/database"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool annotations and prompts are registered during server initialization

const defaultProject = "default"

// MCPServer handles MCP protocol communication
type MCPServer struct {
	server *mcp.Server
	db     *database.DBManager
}

// logToolError emits a consistent, low-cardinality structured log line for tool failures.
// Use key=value formatting to keep logs machine-parseable without introducing a logging dep.
func logToolError(tool string, project string, err error) {
	if err == nil {
		return
	}
	// Note: avoid including dynamic high-cardinality values beyond tool/project.
	log.Printf("level=error tool=%s project=%s msg=tool_failed error=%q", tool, project, err.Error())
}

// NewMCPServer creates a new MCP server
func NewMCPServer(db *database.DBManager) *MCPServer {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-memory-libsql-go",
		Version: buildinfo.Version,
	}, nil)

	mcpServer := &MCPServer{
		server: server,
		db:     db,
	}

	// initialize metrics from env (no-op if disabled)
	metrics.InitFromEnv()
	mcpServer.setupToolHandlers()
	mcpServer.setupPrompts()
	return mcpServer
}

// setupToolHandlers registers all MCP tools
func (s *MCPServer) setupToolHandlers() {
	createEntitiesInputSchema, err := jsonschema.For[apptype.CreateEntitiesArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for CreateEntitiesArgs: %v", err))
	}
	// Tools that return plain text do not need an output schema. Only
	// tools returning structured content should declare OutputSchema.
	searchNodesInputSchema, err := jsonschema.For[apptype.SearchNodesArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for SearchNodesArgs: %v", err))
	}
	searchNodesOutputSchema, err := jsonschema.For[apptype.GraphResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for GraphResult (search): %v", err))
	}
	readGraphInputSchema, err := jsonschema.For[apptype.ReadGraphArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for ReadGraphArgs: %v", err))
	}
	readGraphOutputSchema, err := jsonschema.For[apptype.GraphResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for GraphResult (read): %v", err))
	}
	// Create a fresh GraphResult schema for open_nodes to avoid re-resolving the same root
	openNodesOutputSchema, err := jsonschema.For[apptype.GraphResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for GraphResult (open_nodes): %v", err))
	}
	createRelationsInputSchema, err := jsonschema.For[apptype.CreateRelationsArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for CreateRelationsArgs: %v", err))
	}
	deleteEntityInputSchema, err := jsonschema.For[apptype.DeleteEntityArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for DeleteEntityArgs: %v", err))
	}
	deleteRelationInputSchema, err := jsonschema.For[apptype.DeleteRelationArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for DeleteRelationArgs: %v", err))
	}

	addObservationsInputSchema, err := jsonschema.For[apptype.AddObservationsArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for AddObservationsArgs: %v", err))
	}
	openNodesInputSchema, err := jsonschema.For[apptype.OpenNodesArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for OpenNodesArgs: %v", err))
	}
	deleteEntitiesInputSchema, err := jsonschema.For[apptype.DeleteEntitiesArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for DeleteEntitiesArgs: %v", err))
	}
	deleteRelationsInputSchema, err := jsonschema.For[apptype.DeleteRelationsArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for DeleteRelationsArgs: %v", err))
	}
	deleteObservationsInputSchema, err := jsonschema.For[apptype.DeleteObservationsArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for DeleteObservationsArgs: %v", err))
	}
	updateEntitiesInputSchema, err := jsonschema.For[apptype.UpdateEntitiesArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for UpdateEntitiesArgs: %v", err))
	}
	updateRelationsInputSchema, err := jsonschema.For[apptype.UpdateRelationsArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for UpdateRelationsArgs: %v", err))
	}
	healthInputSchema, err := jsonschema.For[apptype.HealthArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for HealthArgs: %v", err))
	}
	healthOutputSchema, err := jsonschema.For[apptype.HealthResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for HealthResult: %v", err))
	}
	neighborsInputSchema, err := jsonschema.For[apptype.NeighborsArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for NeighborsArgs: %v", err))
	}
	neighborsOutputSchema, err := jsonschema.For[apptype.GraphResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for GraphResult (neighbors): %v", err))
	}
	walkInputSchema, err := jsonschema.For[apptype.WalkArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for WalkArgs: %v", err))
	}
	walkOutputSchema, err := jsonschema.For[apptype.GraphResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for GraphResult (walk): %v", err))
	}
	shortestInputSchema, err := jsonschema.For[apptype.ShortestPathArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for ShortestPathArgs: %v", err))
	}
	shortestOutputSchema, err := jsonschema.For[apptype.GraphResult]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for GraphResult (shortest_path): %v", err))
	}

	createEntitiesAnnotations := mcp.ToolAnnotations{Title: "Create Entities"}
	searchNodesAnnotations := mcp.ToolAnnotations{Title: "Search Nodes"}
	readGraphAnnotations := mcp.ToolAnnotations{Title: "Read Graph"}
	createRelationsAnnotations := mcp.ToolAnnotations{Title: "Create Relations"}
	deleteEntityAnnotations := mcp.ToolAnnotations{Title: "Delete Entity"}
	deleteRelationAnnotations := mcp.ToolAnnotations{Title: "Delete Relation"}
	addObservationsAnnotations := mcp.ToolAnnotations{Title: "Add Observations"}
	openNodesAnnotations := mcp.ToolAnnotations{Title: "Open Nodes"}
	deleteEntitiesAnnotations := mcp.ToolAnnotations{Title: "Delete Entities"}
	deleteRelationsAnnotations := mcp.ToolAnnotations{Title: "Delete Relations"}
	deleteObservationsAnnotations := mcp.ToolAnnotations{Title: "Delete Observations"}
	updateEntitiesAnnotations := mcp.ToolAnnotations{Title: "Update Entities"}
	updateRelationsAnnotations := mcp.ToolAnnotations{Title: "Update Relations"}
	healthCheckAnnotations := mcp.ToolAnnotations{Title: "Health Check"}
	neighborsAnnotations := mcp.ToolAnnotations{Title: "Neighbors"}
	walkAnnotations := mcp.ToolAnnotations{Title: "Graph Walk"}
	shortestPathAnnotations := mcp.ToolAnnotations{Title: "Shortest Path"}

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &createEntitiesAnnotations,
		Name:        "create_entities",
		Title:       "Create Entities",
		Description: "Create new entities with observations and optional embeddings.",
		InputSchema: createEntitiesInputSchema,
	}, s.handleCreateEntities)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &searchNodesAnnotations,
		Name:         "search_nodes",
		Title:        "Search Nodes",
		Description:  "Search for entities and their relations using text or vector similarity.",
		InputSchema:  searchNodesInputSchema,
		OutputSchema: searchNodesOutputSchema,
	}, s.handleSearchNodes)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &readGraphAnnotations,
		Name:         "read_graph",
		Title:        "Read Graph",
		Description:  "Get recent entities and their relations.",
		InputSchema:  readGraphInputSchema,
		OutputSchema: readGraphOutputSchema,
	}, s.handleReadGraph)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &createRelationsAnnotations,
		Name:        "create_relations",
		Title:       "Create Relations",
		Description: "Create relations between entities.",
		InputSchema: createRelationsInputSchema,
	}, s.handleCreateRelations)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &deleteEntityAnnotations,
		Name:        "delete_entity",
		Title:       "Delete Entity",
		Description: "Delete an entity and all its associated data (observations and relations).",
		InputSchema: deleteEntityInputSchema,
	}, s.handleDeleteEntity)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &deleteRelationAnnotations,
		Name:        "delete_relation",
		Title:       "Delete Relation",
		Description: "Delete a specific relation between entities.",
		InputSchema: deleteRelationInputSchema,
	}, s.handleDeleteRelation)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &addObservationsAnnotations,
		Name:        "add_observations",
		Title:       "Add Observations",
		Description: "Append observations to an existing entity.",
		InputSchema: addObservationsInputSchema,
	}, s.handleAddObservations)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &openNodesAnnotations,
		Name:         "open_nodes",
		Title:        "Open Nodes",
		Description:  "Retrieve entities by names with optional relations.",
		InputSchema:  openNodesInputSchema,
		OutputSchema: openNodesOutputSchema,
	}, s.handleOpenNodes)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &deleteEntitiesAnnotations,
		Name:        "delete_entities",
		Title:       "Delete Entities",
		Description: "Delete multiple entities by name.",
		InputSchema: deleteEntitiesInputSchema,
	}, s.handleDeleteEntities)
	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &deleteRelationsAnnotations,
		Name:        "delete_relations",
		Title:       "Delete Relations",
		Description: "Delete multiple relations.",
		InputSchema: deleteRelationsInputSchema,
	}, s.handleDeleteRelations)
	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &deleteObservationsAnnotations,
		Name:        "delete_observations",
		Title:       "Delete Observations",
		Description: "Delete observations by id or content for an entity (or all).",
		InputSchema: deleteObservationsInputSchema,
	}, s.handleDeleteObservations)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &updateEntitiesAnnotations,
		Name:        "update_entities",
		Title:       "Update Entities",
		Description: "Partially update entities (type/embedding/observations).",
		InputSchema: updateEntitiesInputSchema,
	}, s.handleUpdateEntities)
	mcp.AddTool(s.server, &mcp.Tool{
		Annotations: &updateRelationsAnnotations,
		Name:        "update_relations",
		Title:       "Update Relations",
		Description: "Update relation tuples via delete/insert.",
		InputSchema: updateRelationsInputSchema,
	}, s.handleUpdateRelations)
	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &healthCheckAnnotations,
		Name:         "health_check",
		Title:        "Health Check",
		Description:  "Returns server and configuration information.",
		InputSchema:  healthInputSchema,
		OutputSchema: healthOutputSchema,
	}, s.handleHealth)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &neighborsAnnotations,
		Name:         "neighbors",
		Title:        "Neighbors",
		Description:  "Fetch 1-hop neighbors for given entities.",
		InputSchema:  neighborsInputSchema,
		OutputSchema: neighborsOutputSchema,
	}, s.handleNeighbors)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &walkAnnotations,
		Name:         "walk",
		Title:        "Graph Walk",
		Description:  "Bounded-depth walk from seed entities.",
		InputSchema:  walkInputSchema,
		OutputSchema: walkOutputSchema,
	}, s.handleWalk)

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &shortestPathAnnotations,
		Name:         "shortest_path",
		Title:        "Shortest Path",
		Description:  "Compute a shortest path between two entities.",
		InputSchema:  shortestInputSchema,
		OutputSchema: shortestOutputSchema,
	}, s.handleShortestPath)
}

// setupPrompts registers MCP prompts to guide clients in using this server
func (s *MCPServer) setupPrompts() {
	// Quick start prompt
	quickStart := &mcp.Prompt{
		Name:        "quick_start",
		Description: "Quick start guidance for using memory tools (search, read, and edit graph).",
	}

	s.server.AddPrompt(quickStart, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Description: "Memory quick start"}, nil
	})

	// Parameterized search guidance prompt
	searchPrompt := &mcp.Prompt{
		Name:        "search_nodes_guidance",
		Description: "Compose an effective memory search using search_nodes",
		Arguments: []*mcp.PromptArgument{
			{Name: "query", Description: "Text query to search (string)", Required: true},
			{Name: "limit", Description: "Max results (default 5)", Required: false},
			{Name: "offset", Description: "Offset for pagination", Required: false},
		},
	}

	s.server.AddPrompt(searchPrompt, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		desc := "Search guidance for search_nodes (text, vector, and hybrid).\n\n" +
			"Text search (FTS5 → LIKE fallback):\n" +
			"- Uses FTS5 when available; transparently falls back to SQL LIKE if FTS5 is missing or a query cannot be parsed.\n" +
			"- Tokenizer includes colon and common symbols: ':' '-' '_' '@' '.' '/' (unicode61).\n" +
			"- Prefix search: append '*' to a token (e.g., Task:*). Works best for tokens length ≥ 2.\n" +
			"- Field qualifiers (FTS only): use entity_name: or content: (e.g., entity_name:\"Repo:\"* OR content:\"P0\").\n" +
			"- Phrases: wrap in double-quotes (e.g., \"design decision\").\n" +
			"- Boolean: space implies AND; use OR explicitly (e.g., alpha OR beta).\n" +
			"- Fallback behavior: on FTS parse errors, the server downgrades this query to LIKE, normalizing '*' to '%' and searching name, type, and observations.\n\n" +
			"- Ranking: FTS results are ordered by BM25 if available (env: BM25_ENABLE, BM25_K1, BM25_B); otherwise by name. LIKE fallback preserves current ordering.\n\n" +
			"Special handling:\n" +
			"- Task:* is treated as a prefix on the literal token 'Task:' across both entity_name and content.\n\n" +
			"Vector search: pass a numeric array matching EMBEDDING_DIMS (float32/float64 or numeric strings).\n\n" +
			"Hybrid search: if HYBRID_SEARCH=true and an embeddings provider is configured, text + vector results are fused using weighted RRF.\n\n" +
			"Examples (JSON tool args):\n" +
			"```json\n" +
			"{ \n  \"query\": \"Task:*\", \"limit\": 10 \n}\n" +
			"```\n" +
			"```json\n" +
			"{ \n  \"query\": \"entity_name:\\\"Repo:\\\"* OR content:\\\"P0\\\"\" \n}\n" +
			"```\n" +
			"```json\n" +
			"{ \n  \"query\": [0.1, 0.2, 0.3, 0.4], \"limit\": 5 \n}\n" +
			"```\n"
		return &mcp.GetPromptResult{Description: desc}, nil
	})

	// KG initialization prompt
	kgInit := &mcp.Prompt{
		Name:        "kg_init_new_repo",
		Description: "Initialize an optimal knowledge graph for a new repository (Repo:*, Pattern:*, Decision:*, Task:* scaffolds, idempotent).",
		Arguments: []*mcp.PromptArgument{
			{Name: "repoSlug", Description: "owner/repo", Required: true},
			{Name: "areas", Description: "Array of focus areas (database,server,embeddings,...)", Required: false},
			{Name: "includeIssues", Description: "Import issues as Task:* with single GitHub link", Required: false},
		},
	}
	s.server.AddPrompt(kgInit, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		desc := "KG Init: create Repo:* and scaffold Pattern:* / Decision:* with tracks/documents relations. Idempotent; do not duplicate names.\n" +
			"\nJSON plan (example):\n" +
			"```json\n" +
			"{\n  \"entities\": [\n    { \"name\": \"Repo: {repoSlug}\", \"entityType\": \"Repo\", \"observations\": [\"Primary repository for KG\"] },\n    { \"name\": \"Pattern: Architecture\", \"entityType\": \"Pattern\", \"observations\": [\"Hexagonal, MCP transports: stdio/SSE\"] }\n  ],\n  \"relations\": [\n    { \"from\": \"Pattern: Architecture\", \"to\": \"Repo: {repoSlug}\", \"relationType\": \"documents\" }\n  ]\n}\n" +
			"```\n\nFlow:\n" +
			"```mermaid\nflowchart TD\n  A[\"Input repoSlug, areas\"] --> B{Repo exists?}\n  B -->|yes| C[Open Repo]\n  B -->|no| D[Create Repo]\n  C --> E[Scaffold Pattern/Decision]\n  D --> E\n  E --> F[create_relations]\n  F --> G[(Optional) Import Issues → Task + GitHub link]\n  G --> H[Done]\n```"
		return &mcp.GetPromptResult{Description: desc}, nil
	})

	// KG update prompt
	kgUpdate := &mcp.Prompt{
		Name:        "kg_update_graph",
		Description: "Update entities/relations with replace/merge observations, add/remove relations with idempotency and low-cardinality texts.",
		Arguments: []*mcp.PromptArgument{
			{Name: "targetNames", Description: "Array of entity names to update", Required: true},
			{Name: "replaceObservations", Description: "Replace observations (array)", Required: false},
			{Name: "mergeObservations", Description: "Merge observations (array)", Required: false},
			{Name: "newRelations", Description: "[{from,to,relationType}]", Required: false},
			{Name: "removeRelations", Description: "[{from,to,relationType}]", Required: false},
		},
	}
	s.server.AddPrompt(kgUpdate, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		desc := "KG Update: open_nodes → diff → update_entities (replace/merge) → create_relations/delete_relations. Normalize texts; avoid duplicates.\n" +
			"\nJSON plan (example):\n" +
			"```json\n" +
			"{\n  \"updates\": [\n    { \"name\": \"Pattern: Architecture\", \"mergeObservations\": [\"Added SSE keep-alive\"] }\n  ],\n  \"relations\": {\n    \"create\": [ { \"from\": \"Decision: Metrics\", \"to\": \"Repo: {repoSlug}\", \"relationType\": \"documents\" } ],\n    \"delete\": [ { \"from\": \"Old\", \"to\": \"X\", \"relationType\": \"tracks\" } ]\n  }\n}\n" +
			"```"
		return &mcp.GetPromptResult{Description: desc}, nil
	})

	// KG sync GitHub links prompt
	kgSync := &mcp.Prompt{
		Name:        "kg_sync_github",
		Description: "Ensure Task:* nodes have exactly one canonical GitHub link observation; dedupe or add as needed.",
		Arguments: []*mcp.PromptArgument{
			{Name: "tasks", Description: "Array of Task:* names", Required: true},
			{Name: "canonicalUrls", Description: "Array of canonical URLs (optional)", Required: false},
		},
	}
	s.server.AddPrompt(kgSync, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		desc := "KG Sync: open_nodes → delete_observations (all GitHub:) → add_observations(single canonical). If no URL provided, fetch externally then set.\n" +
			"\nFlow:\n" +
			"```mermaid\nflowchart TD\n  A[tasks[]] --> B[open_nodes]\n  B --> C{>1 GitHub links?}\n  C -->|yes| D[delete_observations ids]\n  D --> E[add_observations canonical]\n  C -->|no| F{==0?}\n  F -->|yes| E\n  F -->|no| G[Done]\n  E --> G\n```"
		return &mcp.GetPromptResult{Description: desc}, nil
	})

	// KG read prompt (best practices)
	kgRead := &mcp.Prompt{
		Name:        "kg_read_best_practices",
		Description: "Read graph with layered strategy: search_nodes → open_nodes → neighbors/walk/shortest_path; paginate and keep ordering stable.",
		Arguments: []*mcp.PromptArgument{
			{Name: "query", Description: "Text query", Required: true},
			{Name: "limit", Description: "Max results (default 5)", Required: false},
			{Name: "offset", Description: "Offset for pagination", Required: false},
			{Name: "expand", Description: "none|neighbors|walk|shortest_path", Required: false},
			{Name: "direction", Description: "out|in|both", Required: false},
		},
	}
	s.server.AddPrompt(kgRead, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		desc := "KG Read: search_nodes (FTS5 or LIKE fallback) → open_nodes(includeRelations=true) → optional neighbors/walk/shortest_path based on expand.\n" +
			"\nFlow:\n" +
			"```mermaid\nflowchart TD\n  A[query,limit,offset,expand] --> B[search_nodes]\n  B --> C[open_nodes includeRelations]\n  C --> D{expand?}\n  D -->|neighbors| E[neighbors]\n  D -->|walk| F[walk depth 2]\n  D -->|shortest_path| G[shortest_path]\n  D -->|none| H[return]\n  E --> H\n  F --> H\n  G --> H\n```"
		// Append quick query tips
		desc += "\nQuery tips: '*' enables prefix; 'entity_name:'/'content:' qualifiers work with FTS; phrases in quotes; OR supported. 'Task:*' matches tokens starting with 'Task:' across name and content.\n"
		return &mcp.GetPromptResult{Description: desc}, nil
	})

	// Memory operations prompt (user identification + retrieval + update guidance)
	memOps := &mcp.Prompt{
		Name:        "kg_memory_ops_guidance",
		Description: "Operational guidance for memory usage: identify user, retrieve memory, and update KG with new facts.",
	}
	s.server.AddPrompt(memOps, func(ctx context.Context, session *mcp.ServerSession, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
		desc := "Follow these steps per interaction:\n" +
			"1) Assume user=default_user; if unknown, attempt identification.\n" +
			"2) Begin with \"Remembering...\" and retrieve context via read/search tools; refer to KG as \"memory\".\n" +
			"3) Detect new info: identity, behaviors, preferences, goals, relationships.\n" +
			"4) Update memory: create entities for recurring orgs/people/events; relate to current entities; store facts as observations (idempotently).\n" +
			"\nRecommended tool sequence:\n- read_graph(limit=10) or search_nodes(query=topic)\n- open_nodes(includeRelations=true) for specifics\n- update_entities (merge/replace observations)\n- create_relations / delete_relations as needed\n"
		return &mcp.GetPromptResult{Description: desc}, nil
	})
}

func (s *MCPServer) getProjectName(providedName string) string {
	if providedName != "" {
		return providedName
	}
	return defaultProject
}

// handleCreateEntities handles the create_entities tool call
func (s *MCPServer) handleCreateEntities(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.CreateEntitiesArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("create_entities")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	entities := params.Arguments.Entities

	if err := s.db.CreateEntities(ctx, projectName, entities); err != nil {
		success = false
		logToolError("create_entities", projectName, err)
		return nil, fmt.Errorf("failed to create entities: %w", err)
	}
	success = true
	// Observability: record number of entities processed
	metrics.ObserveToolResultSize("create_entities", len(entities))

	result := &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Successfully processed %d entities in project %s", len(entities), projectName),
			},
		},
	}
	return result, nil
}

// handleSearchNodes handles the search_nodes tool call
func (s *MCPServer) handleSearchNodes(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.SearchNodesArgs],
) (*mcp.CallToolResultFor[apptype.GraphResult], error) {
	done := metrics.TimeTool("search_nodes")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	query := params.Arguments.Query
	limit := params.Arguments.Limit
	offset := params.Arguments.Offset
	if limit <= 0 {
		limit = 5
	}
	if offset < 0 {
		offset = 0
	}

	entities, relations, err := s.db.SearchNodes(ctx, projectName, query, limit, offset)
	if err != nil {
		success = false
		logToolError("search_nodes", projectName, err)
		return nil, fmt.Errorf("search failed: %w", err)
	}
	// Normalize to empty arrays to satisfy JSON Schema (avoid null slices)
	if entities == nil {
		entities = []apptype.Entity{}
	}
	if relations == nil {
		relations = []apptype.Relation{}
	}
	success = true
	// Observability: sizes of returned sets
	metrics.ObserveToolResultSize("search_nodes_entities", len(entities))
	metrics.ObserveToolResultSize("search_nodes_relations", len(relations))

	result := &mcp.CallToolResultFor[apptype.GraphResult]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: "Search completed successfully",
			},
		},
		StructuredContent: apptype.GraphResult{
			Entities:  entities,
			Relations: relations,
		},
	}
	return result, nil
}

// handleReadGraph handles the read_graph tool call
func (s *MCPServer) handleReadGraph(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.ReadGraphArgs],
) (*mcp.CallToolResultFor[apptype.GraphResult], error) {
	done := metrics.TimeTool("read_graph")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	limit := params.Arguments.Limit
	if limit <= 0 {
		limit = 10
	}
	entities, relations, err := s.db.ReadGraph(ctx, projectName, limit)
	if err != nil {
		success = false
		logToolError("read_graph", projectName, err)
		return nil, fmt.Errorf("read graph failed: %w", err)
	}
	// Normalize to empty arrays to satisfy JSON Schema (avoid null slices)
	if entities == nil {
		entities = []apptype.Entity{}
	}
	if relations == nil {
		relations = []apptype.Relation{}
	}
	success = true
	metrics.ObserveToolResultSize("read_graph_entities", len(entities))
	metrics.ObserveToolResultSize("read_graph_relations", len(relations))

	result := &mcp.CallToolResultFor[apptype.GraphResult]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: "Graph read successfully",
			},
		},
		StructuredContent: apptype.GraphResult{
			Entities:  entities,
			Relations: relations,
		},
	}
	return result, nil
}

// handleCreateRelations handles the create_relations tool call
func (s *MCPServer) handleCreateRelations(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.CreateRelationsArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("create_relations")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	relations := params.Arguments.Relations

	internalRelations := make([]apptype.Relation, len(relations))
	for i, r := range relations {
		internalRelations[i] = apptype.Relation{
			From:         r.From,
			To:           r.To,
			RelationType: r.RelationType,
		}
	}

	if err := s.db.CreateRelations(ctx, projectName, internalRelations); err != nil {
		success = false
		logToolError("create_relations", projectName, err)
		return nil, fmt.Errorf("failed to create relations: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("create_relations", len(internalRelations))

	result := &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Created %d relations in project %s", len(relations), projectName),
			},
		},
	}
	return result, nil
}

// handleDeleteEntity handles the delete_entity tool call
func (s *MCPServer) handleDeleteEntity(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.DeleteEntityArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("delete_entity")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	name := params.Arguments.Name

	if err := s.db.DeleteEntity(ctx, projectName, name); err != nil {
		success = false
		logToolError("delete_entity", projectName, err)
		return nil, fmt.Errorf("failed to delete entity: %w", err)
	}
	success = true

	result := &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Successfully deleted entity %q in project %s", name, projectName),
			},
		},
	}
	return result, nil
}

// handleDeleteRelation handles the delete_relation tool call
func (s *MCPServer) handleDeleteRelation(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.DeleteRelationArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("delete_relation")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	source := params.Arguments.Source
	target := params.Arguments.Target
	relationType := params.Arguments.Type

	if err := s.db.DeleteRelation(ctx, projectName, source, target, relationType); err != nil {
		success = false
		logToolError("delete_relation", projectName, err)
		return nil, fmt.Errorf("failed to delete relation: %w", err)
	}
	success = true

	result := &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Successfully deleted relation in project %s: %s -> %s (%s)", projectName, source, target, relationType),
			},
		},
	}
	return result, nil
}

// handleAddObservations handles the add_observations tool call
func (s *MCPServer) handleAddObservations(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.AddObservationsArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("add_observations")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	entityName := params.Arguments.EntityName
	observations := params.Arguments.Observations

	if entityName == "" {
		return nil, fmt.Errorf("entityName cannot be empty")
	}
	if len(observations) == 0 {
		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{&mcp.TextContent{Text: "No observations to add"}},
		}, nil
	}

	if err := s.db.AddObservations(ctx, projectName, entityName, observations); err != nil {
		success = false
		logToolError("add_observations", projectName, err)
		return nil, fmt.Errorf("failed to add observations: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("add_observations", len(observations))
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Added %d observations to %q in project %s", len(observations), entityName, projectName)}},
	}, nil
}

// handleOpenNodes handles the open_nodes tool call
func (s *MCPServer) handleOpenNodes(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.OpenNodesArgs],
) (*mcp.CallToolResultFor[apptype.GraphResult], error) {
	done := metrics.TimeTool("open_nodes")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	names := params.Arguments.Names
	include := params.Arguments.IncludeRelations

	entities, err := s.db.GetEntities(ctx, projectName, names)
	if err != nil {
		success = false
		logToolError("open_nodes", projectName, err)
		return nil, fmt.Errorf("failed to get entities: %w", err)
	}
	var relations []apptype.Relation
	if include {
		relations, err = s.db.GetRelationsForEntities(ctx, projectName, entities)
		if err != nil {
			success = false
			logToolError("open_nodes", projectName, err)
			return nil, fmt.Errorf("failed to get relations: %w", err)
		}
	}
	// Normalize to empty arrays for schema compliance
	if entities == nil {
		entities = []apptype.Entity{}
	}
	if relations == nil {
		relations = []apptype.Relation{}
	}
	success = true
	metrics.ObserveToolResultSize("open_nodes_entities", len(entities))
	if include {
		metrics.ObserveToolResultSize("open_nodes_relations", len(relations))
	}
	return &mcp.CallToolResultFor[apptype.GraphResult]{
		Content:           []mcp.Content{&mcp.TextContent{Text: "Open nodes completed"}},
		StructuredContent: apptype.GraphResult{Entities: entities, Relations: relations},
	}, nil
}

// handleDeleteEntities handles bulk entity deletion
func (s *MCPServer) handleDeleteEntities(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.DeleteEntitiesArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("delete_entities")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	names := params.Arguments.Names
	if err := s.db.DeleteEntities(ctx, projectName, names); err != nil {
		success = false
		logToolError("delete_entities", projectName, err)
		return nil, fmt.Errorf("failed to delete entities: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("delete_entities", len(names))
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Deleted %d entities in project %s", len(names), projectName)}},
	}, nil
}

// handleDeleteRelations handles bulk relation deletion
func (s *MCPServer) handleDeleteRelations(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.DeleteRelationsArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("delete_relations")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	tuples := make([]apptype.Relation, len(params.Arguments.Relations))
	for i, r := range params.Arguments.Relations {
		tuples[i] = apptype.Relation(r)
	}
	if err := s.db.DeleteRelations(ctx, projectName, tuples); err != nil {
		success = false
		logToolError("delete_relations", projectName, err)
		return nil, fmt.Errorf("failed to delete relations: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("delete_relations", len(tuples))
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Deleted %d relations in project %s", len(tuples), projectName)}},
	}, nil
}

// handleDeleteObservations handles observation deletion
func (s *MCPServer) handleDeleteObservations(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.DeleteObservationsArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("delete_observations")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	entity := params.Arguments.EntityName
	ids := params.Arguments.IDs
	contents := params.Arguments.Contents
	ra, err := s.db.DeleteObservations(ctx, projectName, entity, ids, contents)
	if err != nil {
		success = false
		logToolError("delete_observations", projectName, err)
		return nil, fmt.Errorf("failed to delete observations: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("delete_observations", int(ra))
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Deleted %d observations from %q in project %s", ra, entity, projectName)}},
	}, nil
}

// handleUpdateEntities updates entities partially
func (s *MCPServer) handleUpdateEntities(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.UpdateEntitiesArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("update_entities")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	if err := s.db.UpdateEntities(ctx, projectName, params.Arguments.Updates); err != nil {
		success = false
		logToolError("update_entities", projectName, err)
		return nil, fmt.Errorf("failed to update entities: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("update_entities", len(params.Arguments.Updates))
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Updated %d entities in project %s", len(params.Arguments.Updates), projectName)}},
	}, nil
}

// handleUpdateRelations updates relation tuples
func (s *MCPServer) handleUpdateRelations(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.UpdateRelationsArgs],
) (*mcp.CallToolResultFor[any], error) {
	done := metrics.TimeTool("update_relations")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	if err := s.db.UpdateRelations(ctx, projectName, params.Arguments.Updates); err != nil {
		success = false
		logToolError("update_relations", projectName, err)
		return nil, fmt.Errorf("failed to update relations: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("update_relations", len(params.Arguments.Updates))
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Updated %d relations in project %s", len(params.Arguments.Updates), projectName)}},
	}, nil
}

// handleHealth returns basic server health information
func (s *MCPServer) handleHealth(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.HealthArgs],
) (*mcp.CallToolResultFor[apptype.HealthResult], error) {
	done := metrics.TimeTool("health_check")
	defer func() { done(true) }()
	cfg := s.db.Config()
	// observe current pool gauges
	inUse, idle := s.db.PoolStats()
	metrics.Default().ObservePoolStats(inUse, idle)
	res := &apptype.HealthResult{
		Name:          "mcp-memory-libsql-go",
		Version:       buildinfo.Version,
		Revision:      buildinfo.Revision,
		BuildDate:     buildinfo.BuildDate,
		MultiProject:  cfg.MultiProjectMode,
		EmbeddingDims: cfg.EmbeddingDims,
	}
	return &mcp.CallToolResultFor[apptype.HealthResult]{
		Content:           []mcp.Content{&mcp.TextContent{Text: "ok"}},
		StructuredContent: *res,
	}, nil
}

// handleNeighbors returns 1-hop neighbors and connecting relations
func (s *MCPServer) handleNeighbors(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.NeighborsArgs],
) (*mcp.CallToolResultFor[apptype.GraphResult], error) {
	done := metrics.TimeTool("neighbors")
	var success bool
	defer func() { done(success) }()
	projectName := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	names := params.Arguments.Names
	direction := params.Arguments.Direction
	limit := params.Arguments.Limit
	ents, rels, err := s.db.GetNeighbors(ctx, projectName, names, direction, limit)
	if err != nil {
		return nil, fmt.Errorf("neighbors failed: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("neighbors_entities", len(ents))
	metrics.ObserveToolResultSize("neighbors_relations", len(rels))
	return &mcp.CallToolResultFor[apptype.GraphResult]{
		Content:           []mcp.Content{&mcp.TextContent{Text: "Neighbors fetched"}},
		StructuredContent: apptype.GraphResult{Entities: ents, Relations: rels},
	}, nil
}

func (s *MCPServer) handleWalk(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.WalkArgs],
) (*mcp.CallToolResultFor[apptype.GraphResult], error) {
	done := metrics.TimeTool("walk")
	var success bool
	defer func() { done(success) }()
	p := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	ents, rels, err := s.db.Walk(ctx, p, params.Arguments.Names, params.Arguments.MaxDepth, params.Arguments.Direction, params.Arguments.Limit)
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("walk_entities", len(ents))
	metrics.ObserveToolResultSize("walk_relations", len(rels))
	return &mcp.CallToolResultFor[apptype.GraphResult]{
		Content:           []mcp.Content{&mcp.TextContent{Text: "Walk complete"}},
		StructuredContent: apptype.GraphResult{Entities: ents, Relations: rels},
	}, nil
}

func (s *MCPServer) handleShortestPath(
	ctx context.Context,
	session *mcp.ServerSession,
	params *mcp.CallToolParamsFor[apptype.ShortestPathArgs],
) (*mcp.CallToolResultFor[apptype.GraphResult], error) {
	done := metrics.TimeTool("shortest_path")
	var success bool
	defer func() { done(success) }()
	p := s.getProjectName(params.Arguments.ProjectArgs.ProjectName)
	ents, rels, err := s.db.ShortestPath(ctx, p, params.Arguments.From, params.Arguments.To, params.Arguments.Direction)
	if err != nil {
		return nil, fmt.Errorf("shortest_path failed: %w", err)
	}
	success = true
	metrics.ObserveToolResultSize("shortest_path_entities", len(ents))
	metrics.ObserveToolResultSize("shortest_path_relations", len(rels))
	return &mcp.CallToolResultFor[apptype.GraphResult]{
		Content:           []mcp.Content{&mcp.TextContent{Text: "Shortest path found"}},
		StructuredContent: apptype.GraphResult{Entities: ents, Relations: rels},
	}, nil
}

// Run starts the MCP server with stdio transport
func (s *MCPServer) Run(ctx context.Context) error {
	// periodic pool stats reporting
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				inUse, idle := s.db.PoolStats()
				metrics.Default().ObservePoolStats(inUse, idle)
			}
		}
	}()
	transport := mcp.NewStdioTransport()
	return s.server.Run(ctx, transport)
}

// RunSSE starts the MCP server over SSE at the given address and endpoint
func (s *MCPServer) RunSSE(ctx context.Context, addr string, endpoint string) error {
	// periodic pool stats reporting
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				inUse, idle := s.db.PoolStats()
				metrics.Default().ObservePoolStats(inUse, idle)
			}
		}
	}()
	// Create the SSE handler and attach a heartbeat to reduce idle disconnects.
	// According to common SSE usage (see MDN and server examples), periodic
	// comments/data keep intermediaries from closing the connection.
	handler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server { return s.server })
	mux := http.NewServeMux()
	// Wrap the SSE handler to set headers that improve stability across proxies.
	mux.HandleFunc(endpoint, func(w http.ResponseWriter, r *http.Request) {
		// Recommended SSE headers
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Disable proxy buffering where applicable (nginx, etc.)
		w.Header().Set("X-Accel-Buffering", "no")
		// Allow simple cross-origin usage for local tools (safe for event stream)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		// Start a lightweight heartbeat goroutine writing SSE comments every 15s.
		// This reduces the chance of idle timeouts during session initialization
		// and long-lived idle periods.
		flusher, _ := w.(http.Flusher)
		doneCh := make(chan struct{})
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-doneCh:
					return
				case <-ticker.C:
					// Write an SSE comment as heartbeat
					_, _ = w.Write([]byte(": keep-alive\n\n"))
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		}()
		// Serve the actual SSE stream
		handler.ServeHTTP(w, r)
		close(doneCh)
	})
	// Avoid server-side timeouts on long-lived SSE connections. Zero means no timeout.
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       0,
		ReadHeaderTimeout: 0,
		WriteTimeout:      0,
		IdleTimeout:       0,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("SSE MCP server listening on %s%s (no server timeouts; keep-alive headers enabled)", addr, endpoint)
	return srv.ListenAndServe()
}
