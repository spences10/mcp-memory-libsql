package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/embeddings"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

// TODO: add a GetRelations method to the DBManager, then update and fix tests that need it

// DBManager handles all database operations
type DBManager struct {
	config *Config
	dbs    map[string]*sql.DB
	mu     sync.RWMutex
	// stmtCache holds prepared statements per project DB: project -> (sql -> *Stmt)
	stmtCache map[string]map[string]*sql.Stmt
	stmtMu    sync.RWMutex
	// capsByProject holds runtime-detected optional capabilities per project
	capMu         sync.RWMutex
	capsByProject map[string]capFlags
	provider      embeddings.Provider
	// search provides strategy-based search (text/vector). Default uses built-ins.
	search SearchStrategy
}

// SetEmbeddingsProvider overrides the embeddings provider (primarily for tests)
func (dm *DBManager) SetEmbeddingsProvider(p embeddings.Provider) {
	dm.provider = p
}

// EnableHybridSearch enables hybrid search strategy with custom weights and k.
func (dm *DBManager) EnableHybridSearch(textWeight, vectorWeight, rrfK float64) {
	if textWeight <= 0 {
		textWeight = 0.4
	}
	if vectorWeight <= 0 {
		vectorWeight = 0.6
	}
	if rrfK <= 0 {
		rrfK = 60
	}
	dm.search = &hybridSearchStrategy{dm: dm, textWeight: textWeight, vectorWeight: vectorWeight, rrfK: rrfK}
}

// DisableHybridSearch restores default (built-in) search behavior.
func (dm *DBManager) DisableHybridSearch() { dm.search = nil }

// PoolStats returns aggregate pool stats across known project DBs.
func (dm *DBManager) PoolStats() (inUse int, idle int) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	for _, db := range dm.dbs {
		s := db.Stats()
		inUse += s.InUse
		idle += s.Idle
	}
	return
}

// GetRelations returns all relations where either source or target belongs to the provided
// entity names. This is a convenience wrapper around GetRelationsForEntities.
func (dm *DBManager) GetRelations(ctx context.Context, projectName string, entityNames []string) ([]apptype.Relation, error) {
	if len(entityNames) == 0 {
		return []apptype.Relation{}, nil
	}
	// Build lightweight entity slice for reuse of existing path
	entities := make([]apptype.Entity, len(entityNames))
	for i, n := range entityNames {
		entities[i] = apptype.Entity{Name: n}
	}
	return dm.GetRelationsForEntities(ctx, projectName, entities)
}

// Config returns a copy of the database configuration
func (dm *DBManager) Config() Config {
	if dm == nil || dm.config == nil {
		return Config{}
	}
	return *dm.config
}

// ValidateProjectAuth enforces per-project authorization in multi-project mode.
// Token is stored under <ProjectsDir>/<projectName>/.auth_token. If missing, a
// non-empty provided token will be written as the initial token. Subsequent calls
// must present the same token. No auth is enforced outside multi-project mode.
func (dm *DBManager) ValidateProjectAuth(projectName string, providedToken string) error {
	if !dm.config.MultiProjectMode {
		return nil
	}
	// Allow optional auth via env toggle
	if v := strings.TrimSpace(os.Getenv("MULTI_PROJECT_AUTH_REQUIRED")); v != "" {
		lv := strings.ToLower(v)
		if lv == "false" || lv == "0" || lv == "off" || lv == "no" {
			return nil
		}
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return fmt.Errorf("project name is required in multi-project mode")
	}
	root := filepath.Join(dm.config.ProjectsDir, projectName)
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("failed to create/access project root: %w", err)
	}
	tokPath := filepath.Join(root, ".auth_token")
	data, err := os.ReadFile(tokPath)
	if os.IsNotExist(err) {
		if strings.TrimSpace(providedToken) == "" {
			// Optionally auto-init token via env
			auto := strings.ToLower(strings.TrimSpace(os.Getenv("MULTI_PROJECT_AUTO_INIT_TOKEN")))
			if auto == "true" || auto == "1" || auto == "on" || auto == "yes" {
				tok := strings.TrimSpace(os.Getenv("MULTI_PROJECT_DEFAULT_TOKEN"))
				if tok == "" {
					// generate random 32-byte token hex
					b := make([]byte, 32)
					if _, rerr := rand.Read(b); rerr == nil {
						tok = hex.EncodeToString(b)
					} else {
						tok = fmt.Sprintf("%d", time.Now().UnixNano())
					}
				}
				if werr := os.WriteFile(tokPath, []byte(tok), 0600); werr != nil {
					return fmt.Errorf("failed to auto-init project auth token: %w", werr)
				}
				// Do not leak the token; require client to provide it on subsequent calls
				return fmt.Errorf("project token initialized; retry with projectArgs.authToken")
			}
			return fmt.Errorf("auth token required for project %s", projectName)
		}
		if werr := os.WriteFile(tokPath, []byte(strings.TrimSpace(providedToken)), 0600); werr != nil {
			return fmt.Errorf("failed to initialize project auth token: %w", werr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read project auth token: %w", err)
	}
	stored := strings.TrimSpace(string(data))
	if stored == "" {
		if strings.TrimSpace(providedToken) == "" {
			return fmt.Errorf("auth token required for project %s", projectName)
		}
		if werr := os.WriteFile(tokPath, []byte(strings.TrimSpace(providedToken)), 0600); werr != nil {
			return fmt.Errorf("failed to set project auth token: %w", werr)
		}
		return nil
	}
	if strings.TrimSpace(providedToken) != stored {
		return fmt.Errorf("unauthorized for project %s", projectName)
	}
	return nil
}

// ensureFTSSchema creates FTS5 virtual table and triggers if supported
func (dm *DBManager) ensureFTSSchema(ctx context.Context, db *sql.DB) error {
	// Recreate FTS table with robust tokenizer and prefix support for queries like "Task:*"
	// - Include ':' in tokenchars so names like "Task:..." are treated as single tokens
	// - Enable prefix search for reasonable term lengths
	stmts := []string{
		`DROP TRIGGER IF EXISTS trg_obs_ai`,
		`DROP TRIGGER IF EXISTS trg_obs_ad`,
		`DROP TRIGGER IF EXISTS trg_obs_au`,
		`DROP TABLE IF EXISTS fts_observations`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS fts_observations USING fts5(
            entity_name,
            content,
            tokenize = 'unicode61 tokenchars=:-_@./',
            prefix = '2 3 4 5 6 7'
        )`,
		`CREATE TRIGGER IF NOT EXISTS trg_obs_ai AFTER INSERT ON observations BEGIN
            INSERT INTO fts_observations(rowid, entity_name, content) VALUES (new.id, new.entity_name, new.content);
        END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_obs_ad AFTER DELETE ON observations BEGIN
            INSERT INTO fts_observations(fts_observations, rowid, entity_name, content) VALUES ('delete', old.id, old.entity_name, old.content);
        END;`,
		`CREATE TRIGGER IF NOT EXISTS trg_obs_au AFTER UPDATE ON observations BEGIN
            INSERT INTO fts_observations(fts_observations, rowid, entity_name, content) VALUES ('delete', old.id, old.entity_name, old.content);
            INSERT INTO fts_observations(rowid, entity_name, content) VALUES (new.id, new.entity_name, new.content);
        END;`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			// If module missing or any error occurs, do not hard-fail server init
			return nil
		}
	}
	// Backfill existing observations into FTS table (idempotent by rowid check)
	_, _ = db.ExecContext(ctx, `INSERT INTO fts_observations(rowid, entity_name, content)
        SELECT o.id, o.entity_name, o.content
        FROM observations o
        WHERE NOT EXISTS (SELECT 1 FROM fts_observations f WHERE f.rowid = o.id)`)
	return nil
}

// GetEntity retrieves a single entity by name
func (dm *DBManager) GetEntity(ctx context.Context, projectName string, name string) (*apptype.Entity, error) {
	done := metrics.TimeOp("db_get_entity")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	stmt, err := dm.getPreparedStmt(ctx, projectName, db, "SELECT name, entity_type, embedding FROM entities WHERE name = ?")
	if err != nil {
		return nil, err
	}
	row := stmt.QueryRowContext(ctx, name)

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

	success = true
	return &apptype.Entity{
		Name:         entityName,
		EntityType:   entityType,
		Observations: observations,
		Embedding:    vector,
	}, nil
}

// GetEntities retrieves a list of entities by names
func (dm *DBManager) GetEntities(ctx context.Context, projectName string, names []string) ([]apptype.Entity, error) {
	done := metrics.TimeOp("db_get_entities")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return []apptype.Entity{}, nil
	}
	placeholders := strings.Repeat("?,", len(names))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf("SELECT name, entity_type, embedding FROM entities WHERE name IN (%s)", placeholders)
	args := make([]interface{}, len(names))
	for i, n := range names {
		args[i] = n
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query entities by names: %w", err)
	}
	defer rows.Close()

	var results []apptype.Entity
	for rows.Next() {
		var name, entityType string
		var embeddingBytes []byte
		if err := rows.Scan(&name, &entityType, &embeddingBytes); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}
		observations, err := dm.getEntityObservations(ctx, projectName, name)
		if err != nil {
			return nil, fmt.Errorf("failed to get observations for %q: %w", name, err)
		}
		vector, err := dm.ExtractVector(ctx, embeddingBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to extract vector for %q: %w", name, err)
		}
		results = append(results, apptype.Entity{
			Name:         name,
			EntityType:   entityType,
			Observations: observations,
			Embedding:    vector,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	success = true
	return results, nil
}

// AddObservations appends observations to an existing entity
func (dm *DBManager) AddObservations(ctx context.Context, projectName string, entityName string, observations []string) error {
	done := metrics.TimeOp("db_add_observations")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(entityName) == "" {
		return fmt.Errorf("entityName cannot be empty")
	}
	if len(observations) == 0 {
		return nil
	}
	// ensure entity exists
	var tmp string
	if err := db.QueryRowContext(ctx, "SELECT name FROM entities WHERE name = ?", entityName).Scan(&tmp); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("entity not found: %s", entityName)
		}
		return fmt.Errorf("failed to verify entity existence: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "INSERT INTO observations (entity_name, content) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare insert observation: %w", err)
	}
	defer stmt.Close()
	for _, obs := range observations {
		if strings.TrimSpace(obs) == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, entityName, obs); err != nil {
			return fmt.Errorf("failed to insert observation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// DeleteEntity deletes an entity and all associated data
func (dm *DBManager) DeleteEntity(ctx context.Context, projectName string, name string) error {
	done := metrics.TimeOp("db_delete_entity")
	success := false
	defer func() { done(success) }()
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

	// Single DELETE on entities; trigger trg_entities_delete_cascade will clean up
	if _, err := db.ExecContext(ctx, "DELETE FROM entities WHERE name = ?", name); err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}
	success = true
	return nil
}

// DeleteRelation deletes a specific relation
func (dm *DBManager) DeleteRelation(ctx context.Context, projectName string, source, target, relationType string) error {
	done := metrics.TimeOp("db_delete_relation")
	success := false
	defer func() { done(success) }()
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

	success = true
	return nil
}

// DeleteEntities deletes multiple entities by name within a single transaction
func (dm *DBManager) DeleteEntities(ctx context.Context, projectName string, names []string) error {
	done := metrics.TimeOp("db_delete_entities")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	// Transactional, chunked bulk delete relying on trigger cascade
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// SQLite has a limit on bound variables (commonly 999). Use conservative chunking.
	const maxParams = 500
	var chunk []string
	for i := 0; i < len(names); i += maxParams {
		end := i + maxParams
		if end > len(names) {
			end = len(names)
		}
		chunk = names[i:end]
		// Build placeholders and args
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = placeholders[:len(placeholders)-1]
		q := fmt.Sprintf("DELETE FROM entities WHERE name IN (%s)", placeholders)
		args := make([]interface{}, len(chunk))
		for j, n := range chunk {
			args[j] = n
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("failed bulk entity delete: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// DeleteRelations deletes multiple relations within a transaction
func (dm *DBManager) DeleteRelations(ctx context.Context, projectName string, tuples []apptype.Relation) error {
	done := metrics.TimeOp("db_delete_relations")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}
	if len(tuples) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM relations WHERE source = ? AND target = ? AND relation_type = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare delete relation: %w", err)
	}
	defer stmt.Close()
	for _, r := range tuples {
		if _, err := stmt.ExecContext(ctx, r.From, r.To, r.RelationType); err != nil {
			return fmt.Errorf("failed to delete relation %s->%s(%s): %w", r.From, r.To, r.RelationType, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// DeleteObservations deletes observations by ids or exact contents for an entity
func (dm *DBManager) DeleteObservations(ctx context.Context, projectName string, entityName string, ids []int64, contents []string) (int64, error) {
	done := metrics.TimeOp("db_delete_observations")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(entityName) == "" {
		return 0, fmt.Errorf("entityName cannot be empty")
	}
	if len(ids) == 0 && len(contents) == 0 {
		// delete all for entity
		res, err := db.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", entityName)
		if err != nil {
			return 0, fmt.Errorf("failed to delete observations: %w", err)
		}
		ra, _ := res.RowsAffected()
		success = true
		return ra, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	var total int64
	const maxParams = 500
	if len(ids) > 0 {
		for i := 0; i < len(ids); i += maxParams {
			end := i + maxParams
			if end > len(ids) {
				end = len(ids)
			}
			chunk := ids[i:end]
			placeholders := strings.Repeat("?,", len(chunk))
			placeholders = placeholders[:len(placeholders)-1]
			args := make([]interface{}, 0, len(chunk)+1)
			args = append(args, entityName)
			for _, id := range chunk {
				args = append(args, id)
			}
			q := fmt.Sprintf("DELETE FROM observations WHERE entity_name = ? AND id IN (%s)", placeholders)
			res, err := tx.ExecContext(ctx, q, args...)
			if err != nil {
				return 0, fmt.Errorf("failed to delete observations by id: %w", err)
			}
			ra, _ := res.RowsAffected()
			total += ra
		}
	}
	if len(contents) > 0 {
		for i := 0; i < len(contents); i += maxParams {
			end := i + maxParams
			if end > len(contents) {
				end = len(contents)
			}
			chunk := contents[i:end]
			placeholders := strings.Repeat("?,", len(chunk))
			placeholders = placeholders[:len(placeholders)-1]
			args := make([]interface{}, 0, len(chunk)+1)
			args = append(args, entityName)
			for _, c := range chunk {
				args = append(args, c)
			}
			q := fmt.Sprintf("DELETE FROM observations WHERE entity_name = ? AND content IN (%s)", placeholders)
			res, err := tx.ExecContext(ctx, q, args...)
			if err != nil {
				// Fallback: select IDs for the given contents and delete by IDs
				idsQ := fmt.Sprintf("SELECT id FROM observations WHERE entity_name = ? AND content IN (%s)", placeholders)
				rows, selErr := tx.QueryContext(ctx, idsQ, args...)
				if selErr != nil {
					return 0, fmt.Errorf("failed to select observation ids for content fallback: %w", selErr)
				}
				var idChunk []int64
				for rows.Next() {
					var id int64
					if scanErr := rows.Scan(&id); scanErr != nil {
						rows.Close()
						return 0, fmt.Errorf("failed to scan observation id: %w", scanErr)
					}
					idChunk = append(idChunk, id)
				}
				if errRows := rows.Err(); errRows != nil {
					rows.Close()
					return 0, fmt.Errorf("error iterating fallback ids: %w", errRows)
				}
				rows.Close()
				// Build args for id delete
				if len(idChunk) > 0 {
					idPH := strings.Repeat("?,", len(idChunk))
					idPH = idPH[:len(idPH)-1]
					idArgs := make([]interface{}, 0, len(idChunk)+1)
					idArgs = append(idArgs, entityName)
					for _, id := range idChunk {
						idArgs = append(idArgs, id)
					}
					delQ := fmt.Sprintf("DELETE FROM observations WHERE entity_name = ? AND id IN (%s)", idPH)
					delRes, delErr := tx.ExecContext(ctx, delQ, idArgs...)
					if delErr != nil {
						return 0, fmt.Errorf("failed to delete observations by id fallback: %w", delErr)
					}
					ra, _ := delRes.RowsAffected()
					total += ra
					continue
				}
				// Nothing selected; proceed without incrementing total
				continue
			}
			ra, _ := res.RowsAffected()
			total += ra
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	success = true
	return total, nil
}

// Close closes all database connections
func (dm *DBManager) Close() error {
	// Close cached prepared statements first to avoid descriptor leaks
	dm.stmtMu.Lock()
	for proj, cache := range dm.stmtCache {
		for sqlText, stmt := range cache {
			if stmt != nil {
				_ = stmt.Close()
			}
			// clear entry
			cache[sqlText] = nil
			delete(cache, sqlText)
		}
		// remove project bucket
		delete(dm.stmtCache, proj)
	}
	dm.stmtMu.Unlock()

	// Now close DB connections
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var errs []error
	for name, db := range dm.dbs {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close database for project %s: %w", name, err))
		}
		delete(dm.dbs, name)
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
