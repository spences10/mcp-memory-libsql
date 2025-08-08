package memory

import (
	"context"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/database"
)

// Service provides a library-first API for memory operations without MCP transport.
type Service struct {
	db *database.DBManager
}

// NewService constructs a Service with the provided config.
func NewService(cfg *Config) (*Service, error) {
	dm, err := database.NewDBManager(cfg.toInternal())
	if err != nil {
		return nil, err
	}
	return &Service{db: dm}, nil
}

// Close releases resources.
func (s *Service) Close() error { return s.db.Close() }

// CreateEntities inserts entities.
func (s *Service) CreateEntities(ctx context.Context, project string, ents []apptype.Entity) error {
	return s.db.CreateEntities(ctx, project, ents)
}

// CreateRelations inserts relations.
func (s *Service) CreateRelations(ctx context.Context, project string, rels []apptype.Relation) error {
	return s.db.CreateRelations(ctx, project, rels)
}

// SearchText performs text search via underlying DB.
func (s *Service) SearchText(ctx context.Context, project string, query string, limit, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.db.SearchNodes(ctx, project, query, limit, offset)
}

// SearchVector performs vector search.
func (s *Service) SearchVector(ctx context.Context, project string, vector []float32, limit, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.db.SearchNodes(ctx, project, vector, limit, offset)
}

// OpenNodes fetches entities (and optionally relations) by names.
func (s *Service) OpenNodes(ctx context.Context, project string, names []string, includeRelations bool) ([]apptype.Entity, []apptype.Relation, error) {
	ents, err := s.db.GetEntities(ctx, project, names)
	if err != nil {
		return nil, nil, err
	}
	if !includeRelations {
		return ents, []apptype.Relation{}, nil
	}
	rels, err := s.db.GetRelationsForEntities(ctx, project, ents)
	if err != nil {
		return nil, nil, err
	}
	return ents, rels, nil
}

// Graph helpers
func (s *Service) Neighbors(ctx context.Context, project string, names []string, direction string, limit int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.db.GetNeighbors(ctx, project, names, direction, limit)
}

func (s *Service) Walk(ctx context.Context, project string, names []string, maxDepth int, direction string, limit int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.db.Walk(ctx, project, names, maxDepth, direction, limit)
}

func (s *Service) ShortestPath(ctx context.Context, project, from, to, direction string) ([]apptype.Entity, []apptype.Relation, error) {
	return s.db.ShortestPath(ctx, project, from, to, direction)
}

// ReadGraph returns recent entities + relations with limit.
func (s *Service) ReadGraph(ctx context.Context, project string, limit int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.db.ReadGraph(ctx, project, limit)
}

// AddObservations appends observations to an entity.
func (s *Service) AddObservations(ctx context.Context, project, entityName string, observations []string) error {
	return s.db.AddObservations(ctx, project, entityName, observations)
}

// Deletes / Updates
func (s *Service) DeleteEntity(ctx context.Context, project, name string) error {
	return s.db.DeleteEntity(ctx, project, name)
}
func (s *Service) DeleteRelation(ctx context.Context, project, source, target, relationType string) error {
	return s.db.DeleteRelation(ctx, project, source, target, relationType)
}
func (s *Service) DeleteEntities(ctx context.Context, project string, names []string) error {
	return s.db.DeleteEntities(ctx, project, names)
}
func (s *Service) DeleteRelations(ctx context.Context, project string, rels []apptype.Relation) error {
	return s.db.DeleteRelations(ctx, project, rels)
}
func (s *Service) DeleteObservations(ctx context.Context, project, entity string, ids []int64, contents []string) (int64, error) {
	return s.db.DeleteObservations(ctx, project, entity, ids, contents)
}
func (s *Service) UpdateEntities(ctx context.Context, project string, updates []apptype.UpdateEntitySpec) error {
	return s.db.UpdateEntities(ctx, project, updates)
}
func (s *Service) UpdateRelations(ctx context.Context, project string, updates []apptype.UpdateRelationChange) error {
	return s.db.UpdateRelations(ctx, project, updates)
}

// Hybrid search toggles
func (s *Service) EnableHybridSearch(textWeight, vectorWeight, rrfK float64) {
	s.db.EnableHybridSearch(textWeight, vectorWeight, rrfK)
}
func (s *Service) DisableHybridSearch() { s.db.DisableHybridSearch() }
