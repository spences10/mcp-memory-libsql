package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"

	_ "github.com/tursodatabase/go-libsql"
	
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
)

// DBManager handles all database operations
type DBManager struct {
	db *sql.DB
}

// NewDBManager creates a new database manager
func NewDBManager(config *Config) (*DBManager, error) {
	var db *sql.DB
	var err error

	if strings.HasPrefix(config.URL, "file:") {
		// Local database
		db, err = sql.Open("libsql", config.URL)
	} else {
		// Remote database
		authURL := config.URL
		if config.AuthToken != "" {
			authURL += "?authToken=" + config.AuthToken
		}
		db, err = sql.Open("libsql", authURL)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create database connector: %w", err)
	}

	manager := &DBManager{
		db: db,
	}

	// Initialize schema
	if err := manager.initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return manager, nil
}

// initialize creates tables and indexes if they don't exist
func (dm *DBManager) initialize() error {
	for _, statement := range schema {
		_, err := dm.db.ExecContext(context.Background(), statement)
		if err != nil {
			return fmt.Errorf("failed to execute schema statement: %w", err)
		}
	}
	return nil
}

// arrayToVectorString converts a float32 array to libSQL vector string format
func arrayToVectorString(numbers []float32) string {
	// If no embedding provided, create a default zero vector
	if len(numbers) == 0 {
		return "[0.0, 0.0, 0.0, 0.0]"
	}

	// Validate vector dimensions match schema (4 dimensions for testing)
	if len(numbers) != 4 {
		log.Printf("Warning: Vector must have exactly 4 dimensions, got %d. Using default zero vector.", len(numbers))
		return "[0.0, 0.0, 0.0, 0.0]"
	}

	// Validate all elements are finite numbers
	sanitizedNumbers := make([]float32, len(numbers))
	for i, n := range numbers {
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
			log.Printf("Invalid vector value detected, using 0.0 instead of: %f", n)
			sanitizedNumbers[i] = 0.0
		} else {
			sanitizedNumbers[i] = n
		}
	}

	// Convert to string format
	strNumbers := make([]string, len(sanitizedNumbers))
	for i, n := range sanitizedNumbers {
		strNumbers[i] = fmt.Sprintf("%f", n)
	}

	return fmt.Sprintf("[%s]", strings.Join(strNumbers, ", "))
}

// extractVector extracts vector from binary format
func (dm *DBManager) extractVector(ctx context.Context, embedding []byte) ([]float32, error) {
	if len(embedding) == 0 {
		return nil, nil
	}

	// For libsql, the embedding might be stored as a BLOB
	// We need to convert it back to float32 array
	// This is a simplified approach - in reality, we might need to parse the BLOB format
	// FIXME: IMPLEMENT THIS 
	return nil, nil
}

// CreateEntities creates or updates entities with their observations
func (dm *DBManager) CreateEntities(ctx context.Context, entities []apptype.Entity) error {
	for _, entity := range entities {
		// Validate entity
		if entity.Name == "" {
			return fmt.Errorf("entity name must be non-empty")
		}
		if entity.EntityType == "" {
			return fmt.Errorf("entity type must be non-empty for entity %q", entity.Name)
		}
		if len(entity.Observations) == 0 {
			return fmt.Errorf("entity %q must have at least one observation", entity.Name)
		}

		// Start transaction
		tx, err := dm.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		// Rollback on error
		defer func() {
			if err != nil {
				tx.Rollback()
			}
		}()

		// Convert embedding to string
		vectorString := arrayToVectorString(entity.Embedding)

		// First try to update
		result, err := tx.ExecContext(ctx,
			"UPDATE entities SET entity_type = ?, embedding = vector(?) WHERE name = ?",
			entity.EntityType, vectorString, entity.Name)
		if err != nil {
			return fmt.Errorf("failed to update entity %q: %w", entity.Name, err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get rows affected for update: %w", err)
		}

		// If no rows affected, do insert
		if rowsAffected == 0 {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO entities (name, entity_type, embedding) VALUES (?, ?, vector(?))",
				entity.Name, entity.EntityType, vectorString)
			if err != nil {
				return fmt.Errorf("failed to insert entity %q: %w", entity.Name, err)
			}
		}

		// Clear old observations
		_, err = tx.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", entity.Name)
		if err != nil {
			return fmt.Errorf("failed to delete old observations for entity %q: %w", entity.Name, err)
		}

		// Add new observations
		for _, observation := range entity.Observations {
			if observation == "" {
				return fmt.Errorf("observation cannot be empty for entity %q", entity.Name)
			}
			_, err = tx.ExecContext(ctx,
				"INSERT INTO observations (entity_name, content) VALUES (?, ?)",
				entity.Name, observation)
			if err != nil {
				return fmt.Errorf("failed to insert observation for entity %q: %w", entity.Name, err)
			}
		}

		// Commit transaction
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction for entity %q: %w", entity.Name, err)
		}
	}

	return nil
}

// SearchSimilar performs vector similarity search
func (dm *DBManager) SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]apptype.SearchResult, error) {
	if len(embedding) == 0 {
		return nil, fmt.Errorf("search embedding cannot be empty")
	}

	vectorString := arrayToVectorString(embedding)

	// Use vector_top_k to find similar entities, excluding zero vectors
	query := `
		SELECT e.name, e.entity_type, e.embedding,
			   vector_distance_cos(e.embedding, vector(?)) as distance
		FROM entities e
		WHERE e.embedding IS NOT NULL
		AND e.embedding != vector('[0.0, 0.0, 0.0, 0.0]')
		ORDER BY distance ASC
		LIMIT ?
	`

	rows, err := dm.db.QueryContext(ctx, query, vectorString, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to execute similarity search: %w", err)
	}
	defer rows.Close()

	var searchResults []apptype.SearchResult
	for rows.Next() {
		var name, entityType string
		var embeddingBytes []byte
		var distance float64

		if err := rows.Scan(&name, &entityType, &embeddingBytes, &distance); err != nil {
			log.Printf("Warning: Failed to scan search result row: %v", err)
			continue
		}

		// Get observations for the entity
		observations, err := dm.getEntityObservations(ctx, name)
		if err != nil {
			log.Printf("Warning: Failed to get observations for entity %q: %v", name, err)
			continue
		}

		// Extract vector
		vector, err := dm.extractVector(ctx, embeddingBytes)
		if err != nil {
			log.Printf("Warning: Failed to extract vector for entity %q: %v", name, err)
			continue
		}

		searchResults = append(searchResults, apptype.SearchResult{
			Entity: apptype.Entity{
				Name:         name,
				EntityType:   entityType,
				Observations: observations,
				Embedding:    vector,
			},
			Distance: distance,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating search results: %w", err)
	}

	return searchResults, nil
}

// getEntityObservations retrieves all observations for an entity
func (dm *DBManager) getEntityObservations(ctx context.Context, entityName string) ([]string, error) {
	rows, err := dm.db.QueryContext(ctx,
		"SELECT content FROM observations WHERE entity_name = ? ORDER BY id", entityName)
	if err != nil {
		return nil, fmt.Errorf("failed to query observations: %w", err)
	}
	defer rows.Close()

	var observations []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, fmt.Errorf("failed to scan observation: %w", err)
		}
		observations = append(observations, content)
	}

	return observations, rows.Err()
}

// GetEntity retrieves a single entity by name
func (dm *DBManager) GetEntity(ctx context.Context, name string) (*apptype.Entity, error) {
	row := dm.db.QueryRowContext(ctx,
		"SELECT name, entity_type, embedding FROM entities WHERE name = ?", name)

	var entityName, entityType string
	var embeddingBytes []byte

	if err := row.Scan(&entityName, &entityType, &embeddingBytes); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("entity not found: %s", name)
		}
		return nil, fmt.Errorf("failed to scan entity: %w", err)
	}

	observations, err := dm.getEntityObservations(ctx, entityName)
	if err != nil {
		return nil, fmt.Errorf("failed to get observations: %w", err)
	}

	vector, err := dm.extractVector(ctx, embeddingBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to extract vector: %w", err)
	}

	return &apptype.Entity{
		Name:         entityName,
		EntityType:   entityType,
		Observations: observations,
		Embedding:    vector,
	}, nil
}

// SearchEntities performs text-based search
func (dm *DBManager) SearchEntities(ctx context.Context, query string) ([]apptype.Entity, error) {
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	searchQuery := fmt.Sprintf("%%%s%%", query)
	rows, err := dm.db.QueryContext(ctx, `
		SELECT DISTINCT e.name, e.entity_type, e.embedding
		FROM entities e
		LEFT JOIN observations o ON e.name = o.entity_name
		WHERE e.name LIKE ? OR e.entity_type LIKE ? OR o.content LIKE ?
	`, searchQuery, searchQuery, searchQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to execute entity search: %w", err)
	}
	defer rows.Close()

	var entities []apptype.Entity
	for rows.Next() {
		var name, entityType string
		var embeddingBytes []byte

		if err := rows.Scan(&name, &entityType, &embeddingBytes); err != nil {
			log.Printf("Warning: Failed to scan entity row: %v", err)
			continue
		}

		observations, err := dm.getEntityObservations(ctx, name)
		if err != nil {
			log.Printf("Warning: Failed to get observations for entity %q: %v", name, err)
			continue
		}

		vector, err := dm.extractVector(ctx, embeddingBytes)
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
		return nil, fmt.Errorf("error iterating entity results: %w", err)
	}

	return entities, nil
}

// GetRecentEntities retrieves recently created entities
func (dm *DBManager) GetRecentEntities(ctx context.Context, limit int) ([]apptype.Entity, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := dm.db.QueryContext(ctx,
		"SELECT name, entity_type, embedding FROM entities ORDER BY created_at DESC LIMIT ?", limit)
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

		observations, err := dm.getEntityObservations(ctx, name)
		if err != nil {
			log.Printf("Warning: Failed to get observations for entity %q: %v", name, err)
			continue
		}

		vector, err := dm.extractVector(ctx, embeddingBytes)
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

// CreateRelations creates multiple relations between entities
func (dm *DBManager) CreateRelations(ctx context.Context, relations []apptype.Relation) error {
	if len(relations) == 0 {
		return nil
	}

	// Prepare batch statements
	tx, err := dm.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO relations (source, target, relation_type) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, relation := range relations {
		if relation.From == "" || relation.To == "" || relation.RelationType == "" {
			return fmt.Errorf("relation fields cannot be empty")
		}

		_, err := stmt.ExecContext(ctx, relation.From, relation.To, relation.RelationType)
		if err != nil {
			return fmt.Errorf("failed to insert relation (%s -> %s): %w", relation.From, relation.To, err)
		}
	}

	return tx.Commit()
}

// DeleteEntity deletes an entity and all associated data
func (dm *DBManager) DeleteEntity(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("entity name cannot be empty")
	}

	// Check if entity exists
	row := dm.db.QueryRowContext(ctx, "SELECT name FROM entities WHERE name = ?", name)
	var existingName string
	if err := row.Scan(&existingName); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("entity not found: %s", name)
		}
		return fmt.Errorf("failed to check entity existence: %w", err)
	}

	// Prepare batch statements for deletion
	tx, err := dm.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete associated observations first (due to foreign key)
	_, err = tx.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to delete observations: %w", err)
	}

	// Delete associated relations (due to foreign key)
	_, err = tx.ExecContext(ctx, "DELETE FROM relations WHERE source = ? OR target = ?", name, name)
	if err != nil {
		return fmt.Errorf("failed to delete relations: %w", err)
	}

	// Delete the entity
	_, err = tx.ExecContext(ctx, "DELETE FROM entities WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	return tx.Commit()
}

// DeleteRelation deletes a specific relation
func (dm *DBManager) DeleteRelation(ctx context.Context, source, target, relationType string) error {
	if source == "" || target == "" || relationType == "" {
		return fmt.Errorf("relation parameters cannot be empty")
	}

	result, err := dm.db.ExecContext(ctx,
		"DELETE FROM relations WHERE source = ? AND target = ? AND relation_type = ?",
		source, target, relationType)
	if err != nil {
		return fmt.Errorf("failed to delete relation: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("relation not found: %s -> %s (%s)", source, target, relationType)
	}

	return nil
}

// GetRelationsForEntities retrieves relations for a list of entities
func (dm *DBManager) GetRelationsForEntities(ctx context.Context, entities []apptype.Entity) ([]apptype.Relation, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	entityNames := make([]string, len(entities))
	for i, e := range entities {
		entityNames[i] = e.Name
	}

	placeholders := strings.Repeat("?,", len(entityNames))
	placeholders = placeholders[:len(placeholders)-1] // Remove trailing comma

	query := fmt.Sprintf(`
		SELECT source, target, relation_type 
		FROM relations 
		WHERE source IN (%s) OR target IN (%s)
	`, placeholders, placeholders)

	// Double the args for both IN clauses
	args := make([]interface{}, len(entityNames)*2)
	for i, name := range entityNames {
		args[i] = name
		args[i+len(entityNames)] = name
	}

	rows, err := dm.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relations: %w", err)
	}
	defer rows.Close()

	var relations []apptype.Relation
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

	return relations, rows.Err()
}

// ReadGraph retrieves recent entities and their relations
func (dm *DBManager) ReadGraph(ctx context.Context) ([]apptype.Entity, []apptype.Relation, error) {
	entities, err := dm.GetRecentEntities(ctx, 10)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get recent entities: %w", err)
	}

	relations, err := dm.GetRelationsForEntities(ctx, entities)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get relations: %w", err)
	}

	return entities, relations, nil
}

// SearchNodes performs either vector or text search based on query type
func (dm *DBManager) SearchNodes(ctx context.Context, query interface{}) ([]apptype.Entity, []apptype.Relation, error) {
	var entities []apptype.Entity
	var err error

	switch q := query.(type) {
	case []float32:
		// Vector similarity search
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		results, err := dm.SearchSimilar(ctx, q, 5)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to perform similarity search: %w", err)
		}
		entities = make([]apptype.Entity, len(results))
		for i, result := range results {
			entities[i] = result.Entity
		}
	case string:
		// Text-based search
		if q == "" {
			return nil, nil, fmt.Errorf("text query cannot be empty")
		}
		entities, err = dm.SearchEntities(ctx, q)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to perform entity search: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf("unsupported query type: %T", query)
	}

	// If no entities found, return empty result
	if len(entities) == 0 {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}

	relations, err := dm.GetRelationsForEntities(ctx, entities)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get relations: %w", err)
	}

	return entities, relations, nil
}

// Close closes the database connection
func (dm *DBManager) Close() error {
	return dm.db.Close()
}
