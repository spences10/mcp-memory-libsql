package database

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/tursodatabase/go-libsql"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
)

const defaultProject = "default"

// DBManager handles all database operations
type DBManager struct {
	config *Config
	dbs    map[string]*sql.DB
	mu     sync.RWMutex
}

// NewDBManager creates a new database manager
func NewDBManager(config *Config) (*DBManager, error) {
	manager := &DBManager{
		config: config,
		dbs:    make(map[string]*sql.DB),
	}

	// If not in multi-project mode, initialize the default database immediately
	if !config.MultiProjectMode {
		_, err := manager.getDB(defaultProject)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize default database: %w", err)
		}
	}

	return manager, nil
}

// getDB retrieves a database connection for a given project, creating it if necessary
func (dm *DBManager) getDB(projectName string) (*sql.DB, error) {
	dm.mu.RLock()
	db, ok := dm.dbs[projectName]
	dm.mu.RUnlock()

	if ok {
		return db, nil
	}

	dm.mu.Lock()
	defer dm.mu.Unlock()

	// Double-check if another goroutine created the DB while we were waiting for the lock
	db, ok = dm.dbs[projectName]
	if ok {
		return db, nil
	}

	var dbURL string
	if dm.config.MultiProjectMode {
		if projectName == "" {
			return nil, fmt.Errorf("project name cannot be empty in multi-project mode")
		}
		dbPath := filepath.Join(dm.config.ProjectsDir, projectName, "libsql.db")
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create project directory for %s: %w", projectName, err)
		}
		dbURL = fmt.Sprintf("file:%s", dbPath)
	} else {
		dbURL = dm.config.URL
	}

	var newDb *sql.DB
	var err error

	if strings.HasPrefix(dbURL, "file:") {
		newDb, err = sql.Open("libsql", dbURL)
	} else {
		authURL := dbURL
		if dm.config.AuthToken != "" {
			authURL += "?authToken=" + dm.config.AuthToken
		}
		newDb, err = sql.Open("libsql", authURL)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create database connector for project %s: %w", projectName, err)
	}

	// Initialize schema
	if err := dm.initialize(newDb); err != nil {
		newDb.Close()
		return nil, fmt.Errorf("failed to initialize database for project %s: %w", projectName, err)
	}

	dm.dbs[projectName] = newDb
	return newDb, nil
}

// initialize creates tables and indexes if they don't exist
func (dm *DBManager) initialize(db *sql.DB) error {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for initialization: %w", err)
	}
	defer tx.Rollback()

	for _, statement := range schema {
		_, err := tx.Exec(statement)
		if err != nil {
			return fmt.Errorf("failed to execute schema statement: %w", err)
		}
	}

	return tx.Commit()
}

// arrayToVectorString converts a float32 array to libSQL vector string format
func arrayToVectorString(numbers []float32) (string, error) {
	// If no embedding provided, create a default zero vector
	if len(numbers) == 0 {
		return "[0.0, 0.0, 0.0, 0.0]", nil
	}

	// Validate vector dimensions match schema (4 dimensions for testing)
	if len(numbers) != 4 {
		return "", fmt.Errorf("vector must have exactly 4 dimensions, got %d. Please provide a 4D vector or omit for default zero vector", len(numbers))
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

	return fmt.Sprintf("[%s]", strings.Join(strNumbers, ", ")), nil
}

// ExtractVector extracts vector from binary format (F32_BLOB)
func (dm *DBManager) ExtractVector(ctx context.Context, embedding []byte) ([]float32, error) {
	if len(embedding) == 0 {
		return nil, nil
	}

	if len(embedding) != 16 {
		return nil, fmt.Errorf("invalid embedding size: expected 16 bytes for 4-dimensional vector, got %d", len(embedding))
	}

	vector := make([]float32, 4)
	for i := range vector {
		bits := binary.LittleEndian.Uint32(embedding[i*4 : (i+1)*4])
		vector[i] = math.Float32frombits(bits)
	}

	return vector, nil
}

// CreateEntities creates or updates entities with their observations
func (dm *DBManager) CreateEntities(ctx context.Context, projectName string, entities []apptype.Entity) error {
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}

	for _, entity := range entities {
		if strings.TrimSpace(entity.Name) == "" {
			return fmt.Errorf("entity name must be a non-empty string")
		}
		if strings.TrimSpace(entity.EntityType) == "" {
			return fmt.Errorf("invalid entity type for entity %q", entity.Name)
		}
		if len(entity.Observations) == 0 {
			return fmt.Errorf("entity %q must have at least one observation", entity.Name)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction for entity %q: %w", entity.Name, err)
		}

		func() {
			defer func() {
				if p := recover(); p != nil {
					tx.Rollback()
					panic(p)
				} else if err != nil {
					tx.Rollback()
				}
			}()

			vectorString, vErr := arrayToVectorString(entity.Embedding)
			if vErr != nil {
				err = fmt.Errorf("failed to convert embedding for entity %q: %w", entity.Name, vErr)
				return
			}

			result, uErr := tx.ExecContext(ctx,
				"UPDATE entities SET entity_type = ?, embedding = vector32(?) WHERE name = ?",
				entity.EntityType, vectorString, entity.Name)
			if uErr != nil {
				err = fmt.Errorf("failed to update entity %q: %w", entity.Name, uErr)
				return
			}

			rowsAffected, raErr := result.RowsAffected()
			if raErr != nil {
				err = fmt.Errorf("failed to get rows affected for update: %w", raErr)
				return
			}

			if rowsAffected == 0 {
				_, iErr := tx.ExecContext(ctx,
					"INSERT INTO entities (name, entity_type, embedding) VALUES (?, ?, vector32(?))",
					entity.Name, entity.EntityType, vectorString)
				if iErr != nil {
					err = fmt.Errorf("failed to insert entity %q: %w", entity.Name, iErr)
					return
				}
			}

			_, dErr := tx.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", entity.Name)
			if dErr != nil {
				err = fmt.Errorf("failed to delete old observations for entity %q: %w", entity.Name, dErr)
				return
			}

			for _, observation := range entity.Observations {
				if observation == "" {
					err = fmt.Errorf("observation cannot be empty for entity %q", entity.Name)
					return
				}
				_, oErr := tx.ExecContext(ctx,
					"INSERT INTO observations (entity_name, content) VALUES (?, ?)",
					entity.Name, observation)
				if oErr != nil {
					err = fmt.Errorf("failed to insert observation for entity %q: %w", entity.Name, oErr)
					return
				}
			}

			err = tx.Commit()
		}()

		if err != nil {
			return err
		}
	}

	return nil
}

// SearchSimilar performs vector similarity search
func (dm *DBManager) SearchSimilar(ctx context.Context, projectName string, embedding []float32, limit int) ([]apptype.SearchResult, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	if len(embedding) == 0 {
		return nil, fmt.Errorf("search embedding cannot be empty")
	}

	vectorString, err := arrayToVectorString(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert search embedding: %w", err)
	}

	query := `
		SELECT e.name, e.entity_type, e.embedding,
			   vector_distance_cos(e.embedding, vector32(?)) as distance
		FROM entities e
		WHERE e.embedding IS NOT NULL
		AND e.embedding != vector('[0.0, 0.0, 0.0, 0.0]')
		ORDER BY distance ASC
		LIMIT ?
	`

	rows, err := db.QueryContext(ctx, query, vectorString, limit)
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
func (dm *DBManager) getEntityObservations(ctx context.Context, projectName string, entityName string) ([]string, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
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
func (dm *DBManager) GetEntity(ctx context.Context, projectName string, name string) (*apptype.Entity, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	row := db.QueryRowContext(ctx,
		"SELECT name, entity_type, embedding FROM entities WHERE name = ?", name)

	var entityName, entityType string
	var embeddingBytes []byte

	if err := row.Scan(&entityName, &entityType, &embeddingBytes); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("entity not found: %s", name)
		}
		return nil, fmt.Errorf("failed to scan entity: %w", err)
	}

	observations, err := dm.getEntityObservations(ctx, projectName, entityName)
	if err != nil {
		return nil, fmt.Errorf("failed to get observations: %w", err)
	}

	vector, err := dm.ExtractVector(ctx, embeddingBytes)
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
func (dm *DBManager) SearchEntities(ctx context.Context, projectName string, query string) ([]apptype.Entity, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	searchQuery := fmt.Sprintf("%%%s%%", query)
	rows, err := db.QueryContext(ctx, `
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
		return nil, fmt.Errorf("error iterating entity results: %w", err)
	}

	return entities, nil
}

// GetRecentEntities retrieves recently created entities
func (dm *DBManager) GetRecentEntities(ctx context.Context, projectName string, limit int) ([]apptype.Entity, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 10
	}

	rows, err := db.QueryContext(ctx,
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

// CreateRelations creates multiple relations between entities
func (dm *DBManager) CreateRelations(ctx context.Context, projectName string, relations []apptype.Relation) error {
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}

	if len(relations) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
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
func (dm *DBManager) DeleteEntity(ctx context.Context, projectName string, name string) error {
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}

	if name == "" {
		return fmt.Errorf("entity name cannot be empty")
	}

	row := db.QueryRowContext(ctx, "SELECT name FROM entities WHERE name = ?", name)
	var existingName string
	if err := row.Scan(&existingName); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("entity not found: %s", name)
		}
		return fmt.Errorf("failed to check entity existence: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to delete observations: %w", err)
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM relations WHERE source = ? OR target = ?", name, name)
	if err != nil {
		return fmt.Errorf("failed to delete relations: %w", err)
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM entities WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	return tx.Commit()
}

// DeleteRelation deletes a specific relation
func (dm *DBManager) DeleteRelation(ctx context.Context, projectName string, source, target, relationType string) error {
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}

	if source == "" || target == "" || relationType == "" {
		return fmt.Errorf("relation parameters cannot be empty")
	}

	result, err := db.ExecContext(ctx,
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
func (dm *DBManager) GetRelationsForEntities(ctx context.Context, projectName string, entities []apptype.Entity) ([]apptype.Relation, error) {
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	if len(entities) == 0 {
		return nil, nil
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
func (dm *DBManager) ReadGraph(ctx context.Context, projectName string) ([]apptype.Entity, []apptype.Relation, error) {
	entities, err := dm.GetRecentEntities(ctx, projectName, 10)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get recent entities: %w", err)
	}

	relations, err := dm.GetRelationsForEntities(ctx, projectName, entities)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get relations: %w", err)
	}

	return entities, relations, nil
}

// SearchNodes performs either vector or text search based on query type
func (dm *DBManager) SearchNodes(ctx context.Context, projectName string, query interface{}) ([]apptype.Entity, []apptype.Relation, error) {
	var entities []apptype.Entity
	var err error

	switch q := query.(type) {
	case []float32:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, q, 5)
		if searchErr != nil {
			return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
		}
		entities = make([]apptype.Entity, len(results))
		for i, result := range results {
			entities[i] = result.Entity
		}
	case string:
		if q == "" {
			return nil, nil, fmt.Errorf("text query cannot be empty")
		}
		entities, err = dm.SearchEntities(ctx, projectName, q)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to perform entity search: %w", err)
		}
	default:
		return nil, nil, fmt.Errorf("unsupported query type: %T", query)
	}

	if len(entities) == 0 {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}

	relations, err := dm.GetRelationsForEntities(ctx, projectName, entities)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get relations: %w", err)
	}

	return entities, relations, nil
}

// Close closes all database connections
func (dm *DBManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var errs []error
	for name, db := range dm.dbs {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close database for project %s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		// Combine multiple errors into one
		errorMessages := make([]string, len(errs))
		for i, err := range errs {
			errorMessages[i] = err.Error()
		}
		return fmt.Errorf("%s", strings.Join(errorMessages, "; "))
	}

	return nil
}
