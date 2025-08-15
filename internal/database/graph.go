package database

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

// GetRecentEntities retrieves recently created entities
func (dm *DBManager) GetRecentEntities(ctx context.Context, projectName string, limit int) ([]apptype.Entity, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}
	stmt, err := dm.getPreparedStmt(ctx, projectName, db, "SELECT name, entity_type, embedding FROM entities ORDER BY created_at DESC, name DESC LIMIT ?")
	if err != nil {
		return nil, err
	}
	rows, err := stmt.QueryContext(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent entities: %w", err)
	}
	defer rows.Close()
	var entities []apptype.Entity
	for rows.Next() {
		var name, entityType string
		var embeddingBytes []byte
		if err := rows.Scan(&name, &entityType, &embeddingBytes); err != nil {
			log.Printf("Warning: Failed to scan recent entity row: %v", err)
			continue
		}
		observations, err := dm.getEntityObservations(ctx, projectName, name)
		if err != nil {
			log.Printf("Warning: Failed to get observations for entity %q: %v", name, err)
			continue
		}
		vector, err := dm.ExtractVector(ctx, embeddingBytes)
		if err != nil {
			log.Printf("Warning: Failed to extract vector for entity %q: %v", name, err)
			continue
		}
		entities = append(entities, apptype.Entity{
			Name:         name,
			EntityType:   entityType,
			Observations: observations,
			Embedding:    vector,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating recent entities: %w", err)
	}
	return entities, nil
}

// GetNeighbors returns 1-hop neighbors for the given entity names.
// direction: "out" (source->target), "in" (target<-source), or "both".
func (dm *DBManager) GetNeighbors(ctx context.Context, projectName string, names []string, direction string, limit int) ([]apptype.Entity, []apptype.Relation, error) {
	done := metrics.TimeOp("db_get_neighbors")
	success := false
	defer func() { done(success) }()
	if len(names) == 0 {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, nil, err
	}
	// Build direction filter
	if direction == "" {
		direction = "both"
	}
	placeholders := strings.Repeat("?,", len(names))
	placeholders = placeholders[:len(placeholders)-1]
	var query string
	switch strings.ToLower(direction) {
	case "out":
		query = fmt.Sprintf(`
            SELECT source, target, relation_type FROM relations
            WHERE source IN (%s)
        `, placeholders)
	case "in":
		query = fmt.Sprintf(`
            SELECT source, target, relation_type FROM relations
            WHERE target IN (%s)
        `, placeholders)
	default: // both
		query = fmt.Sprintf(`
            SELECT source, target, relation_type FROM relations
            WHERE source IN (%s) OR target IN (%s)
        `, placeholders, placeholders)
	}
	args := make([]interface{}, 0, len(names)*2)
	for _, n := range names {
		args = append(args, n)
	}
	if strings.ToLower(direction) == "both" {
		for _, n := range names {
			args = append(args, n)
		}
	}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query neighbor relations: %w", err)
	}
	defer rows.Close()

	rels := make([]apptype.Relation, 0)
	entitySet := make(map[string]struct{})
	for _, n := range names {
		entitySet[n] = struct{}{}
	}
	for rows.Next() {
		var s, t, rt string
		if err := rows.Scan(&s, &t, &rt); err != nil {
			return nil, nil, fmt.Errorf("failed to scan relation: %w", err)
		}
		rels = append(rels, apptype.Relation{From: s, To: t, RelationType: rt})
		entitySet[s] = struct{}{}
		entitySet[t] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// Materialize entities
	allNames := make([]string, 0, len(entitySet))
	for n := range entitySet {
		allNames = append(allNames, n)
	}
	ents, err := dm.GetEntities(ctx, projectName, allNames)
	if err != nil {
		return nil, nil, err
	}
	success = true
	return ents, rels, nil
}

// Walk expands from seed names up to maxDepth using BFS and returns visited entities and edges.
func (dm *DBManager) Walk(ctx context.Context, projectName string, seeds []string, maxDepth int, direction string, limit int) ([]apptype.Entity, []apptype.Relation, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	visited := make(map[string]struct{})
	queue := make([]string, 0, len(seeds))
	queue = append(queue, seeds...)
	for _, s := range seeds {
		visited[s] = struct{}{}
	}
	allRels := make([]apptype.Relation, 0)
	depth := 0
	curr := queue
	for depth < maxDepth && len(curr) > 0 {
		ents, rels, err := dm.GetNeighbors(ctx, projectName, curr, direction, 0)
		if err != nil {
			return nil, nil, err
		}
		allRels = append(allRels, rels...)
		next := make([]string, 0)
		for _, e := range ents {
			if _, ok := visited[e.Name]; ok {
				continue
			}
			visited[e.Name] = struct{}{}
			next = append(next, e.Name)
			if limit > 0 && len(visited) >= limit {
				break
			}
		}
		curr = next
		depth++
		if limit > 0 && len(visited) >= limit {
			break
		}
	}
	// materialize visited entities
	namesList := make([]string, 0, len(visited))
	for n := range visited {
		namesList = append(namesList, n)
	}
	ents, err := dm.GetEntities(ctx, projectName, namesList)
	if err != nil {
		return nil, nil, err
	}
	return ents, allRels, nil
}

// ShortestPath returns a shortest path as entities and relations using BFS edges.
// Note: returns subgraph containing the path; if no path found, returns empty slices.
func (dm *DBManager) ShortestPath(ctx context.Context, projectName, from, to, direction string) ([]apptype.Entity, []apptype.Relation, error) {
	if from == "" || to == "" || from == to {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}
	// BFS parents
	parents := make(map[string]string)
	visited := make(map[string]bool)
	q := []string{from}
	visited[from] = true
	found := false
	for len(q) > 0 && !found {
		level := q
		q = nil
		_, rels, err := dm.GetNeighbors(ctx, projectName, level, direction, 0)
		if err != nil {
			return nil, nil, err
		}
		// Build adjacency from rels by direction
		next := make([]string, 0)
		for _, r := range rels {
			try := func(u, v string) {
				if !visited[v] {
					visited[v] = true
					parents[v] = u
					next = append(next, v)
					if v == to {
						found = true
					}
				}
			}
			switch strings.ToLower(direction) {
			case "out":
				try(r.From, r.To)
			case "in":
				try(r.To, r.From)
			default:
				try(r.From, r.To)
				try(r.To, r.From)
			}
			if found {
				break
			}
		}
		q = append(q, next...)
	}
	if !found {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}
	// reconstruct path
	pathNames := []string{to}
	cur := to
	for cur != from {
		p := parents[cur]
		pathNames = append(pathNames, p)
		cur = p
	}
	// reverse to get from->to order
	for i, j := 0, len(pathNames)-1; i < j; i, j = i+1, j-1 {
		pathNames[i], pathNames[j] = pathNames[j], pathNames[i]
	}
	// materialize entities
	ents, err := dm.GetEntities(ctx, projectName, pathNames)
	if err != nil {
		return nil, nil, err
	}
	// generate relation edges along path in requested direction
	pathRels := make([]apptype.Relation, 0, len(pathNames)-1)
	for i := 0; i+1 < len(pathNames); i++ {
		pathRels = append(pathRels, apptype.Relation{From: pathNames[i], To: pathNames[i+1], RelationType: "path"})
	}
	return ents, pathRels, nil
}

// ReadGraph retrieves recent entities and their relations
func (dm *DBManager) ReadGraph(ctx context.Context, projectName string, limit int) ([]apptype.Entity, []apptype.Relation, error) {
	entities, err := dm.GetRecentEntities(ctx, projectName, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get recent entities: %w", err)
	}

	relations, err := dm.GetRelationsForEntities(ctx, projectName, entities)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get relations: %w", err)
	}

	return entities, relations, nil
}
