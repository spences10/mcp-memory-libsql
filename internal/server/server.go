package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/buildinfo"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/database"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultProject = "default"

// MCPServer handles MCP protocol communication
type MCPServer struct {
	server *mcp.Server
	db     *database.DBManager
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
	return mcpServer
}

// setupToolHandlers registers all MCP tools
func (s *MCPServer) setupToolHandlers() {
	createEntitiesInputSchema, err := jsonschema.For[apptype.CreateEntitiesArgs]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for CreateEntitiesArgs: %v", err))
	}
	anyOutputSchema, err := jsonschema.For[any]()
	if err != nil {
		panic(fmt.Sprintf("failed to create schema for any: %v", err))
	}
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

	createEntitiesAnnotations := mcp.ToolAnnotations{
		Title: "Create Entities",
	}

	mcp.AddTool(s.server, &mcp.Tool{
		Annotations:  &createEntitiesAnnotations,
		Name:         "create_entities",
		Title:        "Create Entities",
		Description:  "Create new entities with observations and optional embeddings.",
		InputSchema:  createEntitiesInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleCreateEntities)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "search_nodes",
		Title:        "Search Nodes",
		Description:  "Search for entities and their relations using text or vector similarity.",
		InputSchema:  searchNodesInputSchema,
		OutputSchema: searchNodesOutputSchema,
	}, s.handleSearchNodes)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "read_graph",
		Title:        "Read Graph",
		Description:  "Get recent entities and their relations.",
		InputSchema:  readGraphInputSchema,
		OutputSchema: readGraphOutputSchema,
	}, s.handleReadGraph)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "create_relations",
		Title:        "Create Relations",
		Description:  "Create relations between entities.",
		InputSchema:  createRelationsInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleCreateRelations)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "delete_entity",
		Title:        "Delete Entity",
		Description:  "Delete an entity and all its associated data (observations and relations).",
		InputSchema:  deleteEntityInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleDeleteEntity)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "delete_relation",
		Title:        "Delete Relation",
		Description:  "Delete a specific relation between entities.",
		InputSchema:  deleteRelationInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleDeleteRelation)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "add_observations",
		Title:        "Add Observations",
		Description:  "Append observations to an existing entity.",
		InputSchema:  addObservationsInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleAddObservations)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "open_nodes",
		Title:        "Open Nodes",
		Description:  "Retrieve entities by names with optional relations.",
		InputSchema:  openNodesInputSchema,
		OutputSchema: openNodesOutputSchema,
	}, s.handleOpenNodes)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "delete_entities",
		Title:        "Delete Entities",
		Description:  "Delete multiple entities by name.",
		InputSchema:  deleteEntitiesInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleDeleteEntities)
	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "delete_relations",
		Title:        "Delete Relations",
		Description:  "Delete multiple relations.",
		InputSchema:  deleteRelationsInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleDeleteRelations)
	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "delete_observations",
		Title:        "Delete Observations",
		Description:  "Delete observations by id or content for an entity (or all).",
		InputSchema:  deleteObservationsInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleDeleteObservations)

	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "update_entities",
		Title:        "Update Entities",
		Description:  "Partially update entities (type/embedding/observations).",
		InputSchema:  updateEntitiesInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleUpdateEntities)
	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "update_relations",
		Title:        "Update Relations",
		Description:  "Update relation tuples via delete/insert.",
		InputSchema:  updateRelationsInputSchema,
		OutputSchema: anyOutputSchema,
	}, s.handleUpdateRelations)
	mcp.AddTool(s.server, &mcp.Tool{
		Name:         "health_check",
		Title:        "Health Check",
		Description:  "Returns server and configuration information.",
		InputSchema:  healthInputSchema,
		OutputSchema: healthOutputSchema,
	}, s.handleHealth)
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
		return nil, fmt.Errorf("failed to create entities: %w", err)
	}
	success = true

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
		return nil, fmt.Errorf("search failed: %w", err)
	}
	success = true

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
		return nil, fmt.Errorf("read graph failed: %w", err)
	}
	success = true

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
		return nil, fmt.Errorf("failed to create relations: %w", err)
	}
	success = true

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
		return nil, fmt.Errorf("failed to add observations: %w", err)
	}
	success = true
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
		return nil, fmt.Errorf("failed to get entities: %w", err)
	}
	var relations []apptype.Relation
	if include {
		relations, err = s.db.GetRelationsForEntities(ctx, projectName, entities)
		if err != nil {
			success = false
			return nil, fmt.Errorf("failed to get relations: %w", err)
		}
	}
	success = true
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
		return nil, fmt.Errorf("failed to delete entities: %w", err)
	}
	success = true
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
		tuples[i] = apptype.Relation{From: r.From, To: r.To, RelationType: r.RelationType}
	}
	if err := s.db.DeleteRelations(ctx, projectName, tuples); err != nil {
		success = false
		return nil, fmt.Errorf("failed to delete relations: %w", err)
	}
	success = true
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
		return nil, fmt.Errorf("failed to delete observations: %w", err)
	}
	success = true
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
		return nil, fmt.Errorf("failed to update entities: %w", err)
	}
	success = true
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
		return nil, fmt.Errorf("failed to update relations: %w", err)
	}
	success = true
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

// Run starts the MCP server with stdio transport
func (s *MCPServer) Run(ctx context.Context) error {
	transport := mcp.NewStdioTransport()
	return s.server.Run(ctx, transport)
}

// RunSSE starts the MCP server over SSE at the given address and endpoint
func (s *MCPServer) RunSSE(ctx context.Context, addr string, endpoint string) error {
	handler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server { return s.server })
	mux := http.NewServeMux()
	mux.Handle(endpoint, handler)
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("SSE MCP server listening on %s%s", addr, endpoint)
	return srv.ListenAndServe()
}
