package database

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

// GetRecentEntities retrieves the most recently created entities up to limit.
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

// GetRelationsForEntities returns all relations touching any of the provided entities.
func (dm *DBManager) GetRelationsForEntities(ctx context.Context, projectName string, entities []apptype.Entity) ([]apptype.Relation, error) {
	done := metrics.TimeOp("db_get_relations_for_entities")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}
	if len(entities) == 0 {
		return []apptype.Relation{}, nil
	}
	entityNames := make([]string, len(entities))
	for i, e := range entities {
		entityNames[i] = e.Name
	}
	placeholders := strings.Repeat("?,", len(entityNames))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf(`
		SELECT source, target, relation_type 
		FROM relations 
		WHERE source IN (%s) OR target IN (%s)
	`, placeholders, placeholders)
	args := make([]interface{}, len(entityNames)*2)
	for i, name := range entityNames {
		args[i] = name
		args[i+len(entityNames)] = name
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relations: %w", err)
	}
	defer rows.Close()
	relations := make([]apptype.Relation, 0)
	for rows.Next() {
		var source, target, relationType string
		if err := rows.Scan(&source, &target, &relationType); err != nil {
			return nil, fmt.Errorf("failed to scan relation: %w", err)
		}
		relations = append(relations, apptype.Relation{
			From:         source,
			To:           target,
			RelationType: relationType,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	success = true
	return relations, nil
}

// GetNeighbors returns 1-hop neighbors and the connecting relations for given names.
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
	default:
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

// Walk traverses outward from seed entities up to maxDepth and returns the subgraph.
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

// ShortestPath finds one shortest path (if any) between two entities and returns the path subgraph.
//
// This function uses the Breadth-First Search (BFS) algorithm to find the shortest path
// between the 'from' and 'to' entities in the graph. BFS explores the graph level by level,
// ensuring that the first path found is one of the shortest possible paths.
//
// The returned relations use the RelationType "path" to indicate that these edges are part
// of the discovered shortest path between the two entities, rather than representing the
// original relation type in the graph.
//
// Parameters:
//   - ctx: context for cancellation and deadlines
//   - projectName: the name of the project/graph
//   - from: the starting entity name
//   - to: the target entity name
//   - direction: the direction of traversal ("out", "in", etc.)
//
// Returns:
//   - []apptype.Entity: the entities along the shortest path (including endpoints)
//   - []apptype.Relation: the relations along the path, with RelationType "path"
//   - error: error if any occurred during traversal
func (dm *DBManager) ShortestPath(ctx context.Context, projectName, from, to, direction string) ([]apptype.Entity, []apptype.Relation, error) {
	if from == "" || to == "" || from == to {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}
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
	pathNames := []string{to}
	cur := to
	for cur != from {
		p := parents[cur]
		pathNames = append(pathNames, p)
		cur = p
	}
	for i, j := 0, len(pathNames)-1; i < j; i, j = i+1, j-1 {
		pathNames[i], pathNames[j] = pathNames[j], pathNames[i]
	}
	ents, err := dm.GetEntities(ctx, projectName, pathNames)
	if err != nil {
		return nil, nil, err
	}
	pathRels := make([]apptype.Relation, 0, len(pathNames)-1)
	for i := 0; i+1 < len(pathNames); i++ {
		pathRels = append(pathRels, apptype.Relation{From: pathNames[i], To: pathNames[i+1], RelationType: "path"})
	}
	return ents, pathRels, nil
}

// ReadGraph returns a recent subgraph snapshot with entities and their relations.
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
