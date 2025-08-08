package database

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/tursodatabase/go-libsql"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/embeddings"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

// TODO: add a GetRelations method to the DBManager, then update and fix tests that need it

// SearchStrategy allows pluggable search implementations (text/vector/hybrid)
type SearchStrategy interface {
	Search(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error)
}

// defaultSearchStrategy uses built-in SearchSimilar and SearchEntities paths
type defaultSearchStrategy struct{ dm *DBManager }

func (s *defaultSearchStrategy) Search(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.dm.searchNodesInternal(ctx, projectName, query, limit, offset)
}

// hybridSearchStrategy fuses text (FTS/LIKE) and vector results using RRF-like scoring
type hybridSearchStrategy struct {
	dm           *DBManager
	textWeight   float64
	vectorWeight float64
	rrfK         float64
}

func newHybridSearchStrategy(dm *DBManager) *hybridSearchStrategy {
	wText := 0.4
	wVec := 0.6
	k := 60.0
	if v := os.Getenv("HYBRID_TEXT_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			wText = f
		}
	}
	if v := os.Getenv("HYBRID_VECTOR_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			wVec = f
		}
	}
	if v := os.Getenv("HYBRID_RRF_K"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			k = f
		}
	}
	return &hybridSearchStrategy{dm: dm, textWeight: wText, vectorWeight: wVec, rrfK: k}
}

func (s *hybridSearchStrategy) Search(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	// Only perform hybrid when query is text and we can produce a vector (provider present)
	qStr, ok := query.(string)
	if !ok || strings.TrimSpace(qStr) == "" {
		// Fallback to default behavior for non-text queries
		return s.dm.searchNodesInternal(ctx, projectName, query, limit, offset)
	}

	// Collect text results
	// Pull extra results (limit+offset) then slice after fusion
	fetch := limit + offset
	if fetch <= 0 {
		fetch = limit
	}
	if fetch <= 0 {
		fetch = 10
	}
	textResults, tErr := s.dm.SearchEntities(ctx, projectName, qStr, fetch, 0)
	if tErr != nil {
		return nil, nil, tErr
	}

	// Optionally compute vector results if provider available and dims match
	var vecResults []apptype.SearchResult
	if s.dm.provider != nil && s.dm.provider.Dimensions() == s.dm.config.EmbeddingDims {
		vecs, pErr := s.dm.provider.Embed(ctx, []string{qStr})
		if pErr == nil && len(vecs) == 1 {
			vr, vErr := s.dm.SearchSimilar(ctx, projectName, vecs[0], fetch, 0)
			if vErr == nil {
				vecResults = vr
			}
		}
	}

	// Build ranking maps
	type scored struct {
		entity apptype.Entity
		score  float64
	}
	// Ranks start at 1
	textRank := make(map[string]int)
	for i, e := range textResults {
		textRank[e.Name] = i + 1
	}
	vecRank := make(map[string]int)
	for i, r := range vecResults {
		vecRank[r.Entity.Name] = i + 1
	}
	union := make(map[string]apptype.Entity)
	for _, e := range textResults {
		union[e.Name] = e
	}
	for _, r := range vecResults {
		if _, ok := union[r.Entity.Name]; !ok {
			union[r.Entity.Name] = r.Entity
		}
	}
	// Score with weighted RRF
	scoredList := make([]scored, 0, len(union))
	for name, ent := range union {
		ts := 0.0
		if r, ok := textRank[name]; ok {
			ts = 1.0 / (s.rrfK + float64(r))
		}
		vs := 0.0
		if r, ok := vecRank[name]; ok {
			vs = 1.0 / (s.rrfK + float64(r))
		}
		score := s.textWeight*ts + s.vectorWeight*vs
		scoredList = append(scoredList, scored{entity: ent, score: score})
	}
	sort.SliceStable(scoredList, func(i, j int) bool { return scoredList[i].score > scoredList[j].score })

	// Apply pagination
	start := offset
	if start > len(scoredList) {
		start = len(scoredList)
	}
	end := min(start+limit, len(scoredList))
	entities := make([]apptype.Entity, end-start)
	for i := start; i < end; i++ {
		entities[i-start] = scoredList[i].entity
	}
	if len(entities) == 0 {
		return []apptype.Entity{}, []apptype.Relation{}, nil
	}
	relations, rErr := s.dm.GetRelationsForEntities(ctx, projectName, entities)
	if rErr != nil {
		return nil, nil, rErr
	}
	return entities, relations, nil
}

const defaultProject = "default"

// DBManager handles all database operations
type DBManager struct {
	config *Config
	dbs    map[string]*sql.DB
	mu     sync.RWMutex
	// stmtCache holds prepared statements per project DB: project -> (sql -> *Stmt)
	stmtCache map[string]map[string]*sql.Stmt
	stmtMu    sync.RWMutex
	// caps holds runtime-detected optional capabilities
	caps struct {
		checked    bool
		vectorTopK bool
		fts5       bool
	}
	provider embeddings.Provider
	// search provides strategy-based search (text/vector). Default uses built-ins.
	search SearchStrategy
}

// SetEmbeddingsProvider overrides the embeddings provider (primarily for tests)
func (dm *DBManager) SetEmbeddingsProvider(p embeddings.Provider) {
	dm.provider = p
}

// Config returns a copy of the database configuration
func (dm *DBManager) Config() Config {
	if dm == nil || dm.config == nil {
		return Config{}
	}
	return *dm.config
}

// NewDBManager creates a new database manager
func NewDBManager(config *Config) (*DBManager, error) {
	if config.EmbeddingDims <= 0 || config.EmbeddingDims > 65536 {
		return nil, fmt.Errorf("{\"error\":{\"code\":\"INVALID_EMBEDDING_DIMS\",\"message\":\"EMBEDDING_DIMS must be between 1 and 65536 inclusive\",\"value\":%d}}", config.EmbeddingDims)
	}
	manager := &DBManager{
		config:    config,
		dbs:       make(map[string]*sql.DB),
		stmtCache: make(map[string]map[string]*sql.Stmt),
	}
	manager.provider = embeddings.NewFromEnv()
	// Choose search strategy (default or hybrid via env)
	if strings.EqualFold(os.Getenv("HYBRID_SEARCH"), "true") || os.Getenv("HYBRID_SEARCH") == "1" {
		manager.search = newHybridSearchStrategy(manager)
	} else {
		manager.search = &defaultSearchStrategy{dm: manager}
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
			dm.mu.Unlock()
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
			// Build URL safely and append/override the authToken parameter
			if u, perr := url.Parse(dbURL); perr == nil {
				q := u.Query()
				q.Set("authToken", dm.config.AuthToken)
				u.RawQuery = q.Encode()
				authURL = u.String()
			} else {
				// Fallback: naive append with encoding
				if strings.Contains(dbURL, "?") {
					authURL = dbURL + "&authToken=" + url.QueryEscape(dm.config.AuthToken)
				} else {
					authURL = dbURL + "?authToken=" + url.QueryEscape(dm.config.AuthToken)
				}
			}
		}
		newDb, err = sql.Open("libsql", authURL)
	}

	if err != nil {
		dm.mu.Unlock()
		return nil, fmt.Errorf("failed to create database connector for project %s: %w", projectName, err)
	}

	// Initialize schema
	if err := dm.initialize(newDb); err != nil {
		newDb.Close()
		dm.mu.Unlock()
		return nil, fmt.Errorf("failed to initialize database for project %s: %w", projectName, err)
	}

	// Apply connection pool tuning from config
	if dm.config.MaxOpenConns > 0 {
		newDb.SetMaxOpenConns(dm.config.MaxOpenConns)
	}
	if dm.config.MaxIdleConns > 0 {
		newDb.SetMaxIdleConns(dm.config.MaxIdleConns)
	}
	if dm.config.ConnMaxIdleSec > 0 {
		newDb.SetConnMaxIdleTime(time.Duration(dm.config.ConnMaxIdleSec) * time.Second)
	}
	if dm.config.ConnMaxLifeSec > 0 {
		newDb.SetConnMaxLifetime(time.Duration(dm.config.ConnMaxLifeSec) * time.Second)
	}

	dm.dbs[projectName] = newDb
	// initialize statement cache bucket for this project if not exists
	dm.stmtMu.Lock()
	if _, ok := dm.stmtCache[projectName]; !ok {
		dm.stmtCache[projectName] = make(map[string]*sql.Stmt)
	}
	dm.stmtMu.Unlock()
	// Unlock before capability detection to avoid self-deadlock
	dm.mu.Unlock()
	// Detect optional capabilities once
	dm.detectCapabilities(context.Background(), newDb)
	return newDb, nil
}

// getPreparedStmt returns or prepares and caches a statement for the given project DB
func (dm *DBManager) getPreparedStmt(ctx context.Context, projectName string, db *sql.DB, sqlText string) (*sql.Stmt, error) {
	// fast path read
	dm.stmtMu.RLock()
	if projCache, ok := dm.stmtCache[projectName]; ok {
		if stmt, ok2 := projCache[sqlText]; ok2 {
			dm.stmtMu.RUnlock()
			return stmt, nil
		}
	}
	dm.stmtMu.RUnlock()

	// prepare and store
	stmt, err := db.PrepareContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	dm.stmtMu.Lock()
	if _, ok := dm.stmtCache[projectName]; !ok {
		dm.stmtCache[projectName] = make(map[string]*sql.Stmt)
	}
	dm.stmtCache[projectName][sqlText] = stmt
	dm.stmtMu.Unlock()
	return stmt, nil
}

// initialize creates tables and indexes if they don't exist
func (dm *DBManager) initialize(db *sql.DB) error {
	done := metrics.TimeOp("db_initialize")
	success := false
	defer func() { done(success) }()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for initialization: %w", err)
	}
	defer tx.Rollback()

	for _, statement := range dynamicSchema(dm.config.EmbeddingDims) {
		_, err := tx.Exec(statement)
		if err != nil {
			return fmt.Errorf("failed to execute schema statement: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// detectCapabilities probes presence of vector_top_k and records flags.
func (dm *DBManager) detectCapabilities(ctx context.Context, db *sql.DB) {
	dm.mu.Lock()
	if dm.caps.checked {
		dm.mu.Unlock()
		return
	}
	dm.mu.Unlock()

	// Skip ANN probe for in-memory test URLs to avoid driver quirks
	if strings.Contains(dm.config.URL, "mode=memory") {
		dm.mu.Lock()
		dm.caps.vectorTopK = false
		dm.caps.checked = true
		dm.mu.Unlock()
		return
	}

	zero := dm.vectorZeroString()
	// Attempt to call vector_top_k with a short timeout; close rows if opened
	ctx2, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rows, err := db.QueryContext(ctx2, "SELECT id FROM vector_top_k('idx_entities_embedding', vector32(?), 1) LIMIT 1", zero)
	if rows != nil {
		rows.Close()
	}
	dm.mu.Lock()
	dm.caps.vectorTopK = (err == nil)
	dm.caps.checked = true
	dm.mu.Unlock()

	// Detect FTS5 support by attempting to create a temporary virtual table
	ctx3, cancel3 := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel3()
	if _, err := db.ExecContext(ctx3, "CREATE VIRTUAL TABLE IF NOT EXISTS temp._fts5_probe USING fts5(x)"); err == nil {
		// Clean up probe table
		_, _ = db.ExecContext(ctx3, "DROP TABLE IF EXISTS temp._fts5_probe")
		dm.mu.Lock()
		dm.caps.fts5 = true
		dm.mu.Unlock()
		// Ensure FTS schema/triggers exist for observations
		_ = dm.ensureFTSSchema(context.Background(), db)
	} else {
		dm.mu.Lock()
		dm.caps.fts5 = false
		dm.mu.Unlock()
	}
}

// ensureFTSSchema creates FTS5 virtual table and triggers if supported
func (dm *DBManager) ensureFTSSchema(ctx context.Context, db *sql.DB) error {
	// Create FTS table and triggers; IF NOT EXISTS makes this idempotent
	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS fts_observations USING fts5(entity_name, content)`,
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

// vectorZeroString builds a zero vector string for current embedding dims
func (dm *DBManager) vectorZeroString() string {
	if dm.config.EmbeddingDims <= 0 {
		return "[0.0, 0.0, 0.0, 0.0]"
	}
	parts := make([]string, dm.config.EmbeddingDims)
	for i := range parts {
		parts[i] = "0.0"
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
}

// embeddingInputForEntity builds a deterministic text for provider embedding generation
func (dm *DBManager) embeddingInputForEntity(e apptype.Entity) string {
	// Simple heuristic: join observations; providers often expect natural text
	if len(e.Observations) == 0 {
		return e.Name
	}
	return strings.Join(e.Observations, "\n")
}

// vectorToString converts a float32 array to libSQL vector string format
func (dm *DBManager) vectorToString(numbers []float32) (string, error) {
	// If no embedding provided, create a default zero vector
	if len(numbers) == 0 {
		return dm.vectorZeroString(), nil
	}

	// Validate vector dimensions match schema (use configured dims)
	dims := dm.config.EmbeddingDims
	if dims <= 0 {
		dims = 4
	}
	if len(numbers) != dims {
		return "", fmt.Errorf("vector must have exactly %d dimensions, got %d", dims, len(numbers))
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

	dims := dm.config.EmbeddingDims
	if dims <= 0 {
		dims = 4
	}
	expectedBytes := dims * 4
	if len(embedding) != expectedBytes {
		return nil, fmt.Errorf("invalid embedding size: expected %d bytes for %d-dimensional vector, got %d", expectedBytes, dims, len(embedding))
	}

	vector := make([]float32, dims)
	for i := 0; i < dims; i++ {
		bits := binary.LittleEndian.Uint32(embedding[i*4 : (i+1)*4])
		vector[i] = math.Float32frombits(bits)
	}

	return vector, nil
}

// CreateEntities creates or updates entities with their observations
func (dm *DBManager) CreateEntities(ctx context.Context, projectName string, entities []apptype.Entity) error {
	done := metrics.TimeOp("db_create_entities")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}

	// Auto-generate embeddings via provider when missing
	if dm.provider != nil {
		if dm.provider.Dimensions() != dm.config.EmbeddingDims {
			return fmt.Errorf("{\"error\":{\"code\":\"EMBEDDING_DIMS_MISMATCH\",\"message\":\"Provider dims %d do not match EMBEDDING_DIMS %d\"}}", dm.provider.Dimensions(), dm.config.EmbeddingDims)
		}
		inputs := make([]string, 0)
		idxs := make([]int, 0)
		for i, e := range entities {
			if len(e.Embedding) == 0 {
				inputs = append(inputs, dm.embeddingInputForEntity(e))
				idxs = append(idxs, i)
			}
		}
		if len(inputs) > 0 {
			vecs, pErr := dm.provider.Embed(ctx, inputs)
			if pErr != nil {
				return fmt.Errorf("{\"error\":{\"code\":\"EMBEDDINGS_PROVIDER_ERROR\",\"message\":%q}}", pErr.Error())
			}
			if len(vecs) != len(inputs) {
				return fmt.Errorf("{\"error\":{\"code\":\"EMBEDDINGS_PROVIDER_ERROR\",\"message\":\"provider returned mismatched embeddings count\"}}")
			}
			for j, idx := range idxs {
				entities[idx].Embedding = vecs[j]
			}
		}
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

			vectorString, vErr := dm.vectorToString(entity.Embedding)
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

	success = true
	return nil
}

// UpdateEntities applies partial updates to entities
func (dm *DBManager) UpdateEntities(ctx context.Context, projectName string, updates []apptype.UpdateEntitySpec) error {
	done := metrics.TimeOp("db_update_entities")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, u := range updates {
		if strings.TrimSpace(u.Name) == "" {
			return fmt.Errorf("update missing entity name")
		}
		// Ensure entity exists
		var exists string
		if err := tx.QueryRowContext(ctx, "SELECT name FROM entities WHERE name = ?", u.Name).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("entity not found: %s", u.Name)
			}
			return fmt.Errorf("failed to lookup entity %q: %w", u.Name, err)
		}

		if u.EntityType != "" || len(u.Embedding) > 0 {
			vecStr, vErr := dm.vectorToString(u.Embedding)
			if vErr != nil {
				return fmt.Errorf("embedding conversion failed for %q: %w", u.Name, vErr)
			}
			// If embedding not provided, keep existing by setting to current value (use COALESCE)
			// Here we update both fields if provided; if not, we keep old values.
			if u.EntityType != "" && len(u.Embedding) > 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE entities SET entity_type = ?, embedding = vector32(?) WHERE name = ?", u.EntityType, vecStr, u.Name); err != nil {
					return fmt.Errorf("failed updating entity %q: %w", u.Name, err)
				}
			} else if u.EntityType != "" {
				if _, err := tx.ExecContext(ctx, "UPDATE entities SET entity_type = ? WHERE name = ?", u.EntityType, u.Name); err != nil {
					return fmt.Errorf("failed updating entity type %q: %w", u.Name, err)
				}
			} else if len(u.Embedding) > 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE entities SET embedding = vector32(?) WHERE name = ?", vecStr, u.Name); err != nil {
					return fmt.Errorf("failed updating entity embedding %q: %w", u.Name, err)
				}
			}
		}

		// If embedding still missing and provider exists, generate and update
		if dm.provider != nil && len(u.Embedding) == 0 && len(u.ReplaceObservations) > 0 {
			if dm.provider.Dimensions() != dm.config.EmbeddingDims {
				return fmt.Errorf("{\"error\":{\"code\":\"EMBEDDING_DIMS_MISMATCH\",\"message\":\"Provider dims %d do not match EMBEDDING_DIMS %d\"}}", dm.provider.Dimensions(), dm.config.EmbeddingDims)
			}
			vecs, pErr := dm.provider.Embed(ctx, []string{strings.Join(u.ReplaceObservations, "\n")})
			if pErr != nil {
				return fmt.Errorf("{\"error\":{\"code\":\"EMBEDDINGS_PROVIDER_ERROR\",\"message\":%q}}", pErr.Error())
			}
			if len(vecs) == 1 {
				vecStr, vErr := dm.vectorToString(vecs[0])
				if vErr != nil {
					return fmt.Errorf("embedding conversion failed for %q: %w", u.Name, vErr)
				}
				if _, err := tx.ExecContext(ctx, "UPDATE entities SET embedding = vector32(?) WHERE name = ?", vecStr, u.Name); err != nil {
					return fmt.Errorf("failed updating entity embedding %q: %w", u.Name, err)
				}
			}
		}

		if len(u.ReplaceObservations) > 0 {
			if _, err := tx.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", u.Name); err != nil {
				return fmt.Errorf("failed clearing observations for %q: %w", u.Name, err)
			}
			for _, obs := range u.ReplaceObservations {
				if strings.TrimSpace(obs) == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO observations (entity_name, content) VALUES (?, ?)", u.Name, obs); err != nil {
					return fmt.Errorf("failed inserting observation: %w", err)
				}
			}
		}
		if len(u.MergeObservations) > 0 {
			for _, obs := range u.MergeObservations {
				if strings.TrimSpace(obs) == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO observations (entity_name, content) VALUES (?, ?)", u.Name, obs); err != nil {
					return fmt.Errorf("failed merging observation: %w", err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// UpdateRelations updates relation tuples via delete/insert
func (dm *DBManager) UpdateRelations(ctx context.Context, projectName string, updates []apptype.UpdateRelationChange) error {
	done := metrics.TimeOp("db_update_relations")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	for _, up := range updates {
		// delete old tuple
		if _, err := tx.ExecContext(ctx, "DELETE FROM relations WHERE source = ? AND target = ? AND relation_type = ?", up.From, up.To, up.RelationType); err != nil {
			return fmt.Errorf("failed to delete old relation: %w", err)
		}
		nf := up.NewFrom
		if nf == "" {
			nf = up.From
		}
		nt := up.NewTo
		if nt == "" {
			nt = up.To
		}
		nr := up.NewRelationType
		if nr == "" {
			nr = up.RelationType
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO relations (source, target, relation_type) VALUES (?, ?, ?)", nf, nt, nr); err != nil {
			return fmt.Errorf("failed to insert new relation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// SearchSimilar performs vector similarity search
func (dm *DBManager) SearchSimilar(ctx context.Context, projectName string, embedding []float32, limit int, offset int) ([]apptype.SearchResult, error) {
	done := metrics.TimeOp("db_search_similar")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	if len(embedding) == 0 {
		return nil, fmt.Errorf("search embedding cannot be empty")
	}

	vectorString, err := dm.vectorToString(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert search embedding: %w", err)
	}
	zeroString := dm.vectorZeroString()

	// Prefer vector_top_k if available; fallback to exact ORDER BY path
	dm.mu.RLock()
	useTopK := dm.caps.vectorTopK
	dm.mu.RUnlock()

	var rows *sql.Rows
	if useTopK {
		k := limit + offset
		if k <= 0 {
			k = limit
		}
		topK := `WITH vt AS (
            SELECT id FROM vector_top_k('idx_entities_embedding', vector32(?), ?)
        )
        SELECT e.name, e.entity_type, e.embedding,
               vector_distance_cos(e.embedding, vector32(?)) as distance
        FROM vt JOIN entities e ON e.rowid = vt.id
        WHERE e.embedding IS NOT NULL AND e.embedding != vector32(?)
        ORDER BY distance ASC
        LIMIT ? OFFSET ?`
		stmt, perr := dm.getPreparedStmt(ctx, projectName, db, topK)
		if perr != nil {
			return nil, perr
		}
		rows, err = stmt.QueryContext(ctx, vectorString, k, vectorString, zeroString, limit, offset)
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such function: vector_top_k") {
			// downgrade capability and fall back
			dm.mu.Lock()
			dm.caps.vectorTopK = false
			dm.mu.Unlock()
			useTopK = false
		} else if err != nil {
			return nil, fmt.Errorf("failed ANN search: %w", err)
		}
	}
	if !useTopK {
		query := `SELECT e.name, e.entity_type, e.embedding,
               vector_distance_cos(e.embedding, vector32(?)) as distance
        FROM entities e
        WHERE e.embedding IS NOT NULL
        AND e.embedding != vector32(?)
        ORDER BY distance ASC
        LIMIT ? OFFSET ?`
		stmt, perr := dm.getPreparedStmt(ctx, projectName, db, query)
		if perr != nil {
			return nil, perr
		}
		rows, err = stmt.QueryContext(ctx, vectorString, zeroString, limit, offset)
	}
	if err != nil {
		// Structured error when vector functions unsupported
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "no such function: vector_distance_cos") || strings.Contains(low, "no such function: vector32") {
			return nil, fmt.Errorf("{\"error\":{\"code\":\"VECTOR_SEARCH_UNSUPPORTED\",\"message\":\"Vector search functions are unavailable in this libSQL build\"}}")
		}
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

	success = true
	return searchResults, nil
}

// getEntityObservations retrieves all observations for an entity
func (dm *DBManager) getEntityObservations(ctx context.Context, projectName string, entityName string) ([]string, error) {
	done := metrics.TimeOp("db_get_entity_observations")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	stmt, err := dm.getPreparedStmt(ctx, projectName, db, "SELECT content FROM observations WHERE entity_name = ? ORDER BY id")
	if err != nil {
		return nil, err
	}
	rows, err := stmt.QueryContext(ctx, entityName)
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

	if err := rows.Err(); err != nil {
		return nil, err
	}
	success = true
	return observations, nil
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

// SearchEntities performs text-based search
func (dm *DBManager) SearchEntities(ctx context.Context, projectName string, query string, limit int, offset int) ([]apptype.Entity, error) {
	done := metrics.TimeOp("db_search_entities")
	success := false
	defer func() { done(success) }()
	db, err := dm.getDB(projectName)
	if err != nil {
		return nil, err
	}

	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}

	searchQuery := fmt.Sprintf("%%%s%%", query)
	if limit <= 0 {
		limit = 5
	}
	if offset < 0 {
		offset = 0
	}
	// Prefer FTS5 if available
	dm.mu.RLock()
	useFTS := dm.caps.fts5
	dm.mu.RUnlock()
	var rows *sql.Rows
	if useFTS {
		const qfts = `SELECT DISTINCT e.name, e.entity_type, e.embedding
            FROM fts_observations f
            JOIN observations o ON o.id = f.rowid
            JOIN entities e ON e.name = o.entity_name
            WHERE f.fts_observations MATCH ?
            ORDER BY e.name ASC
            LIMIT ? OFFSET ?`
		stmt, err := dm.getPreparedStmt(ctx, projectName, db, qfts)
		if err != nil {
			return nil, err
		}
		rows, err = stmt.QueryContext(ctx, query, limit, offset)
		if err != nil {
			low := strings.ToLower(err.Error())
			if strings.Contains(low, "no such module: fts5") || strings.Contains(low, "malformed MATCH") {
				// downgrade to LIKE path
				dm.mu.Lock()
				dm.caps.fts5 = false
				dm.mu.Unlock()
				useFTS = false
			} else {
				return nil, fmt.Errorf("failed to execute FTS search: %w", err)
			}
		}
	}
	if !useFTS {
		const q = `SELECT DISTINCT e.name, e.entity_type, e.embedding
            FROM entities e
            LEFT JOIN observations o ON e.name = o.entity_name
            WHERE e.name LIKE ? OR e.entity_type LIKE ? OR o.content LIKE ?
            ORDER BY e.name ASC
            LIMIT ? OFFSET ?`
		stmt, err := dm.getPreparedStmt(ctx, projectName, db, q)
		if err != nil {
			return nil, err
		}
		rows, err = stmt.QueryContext(ctx, searchQuery, searchQuery, searchQuery, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("failed to execute entity search: %w", err)
		}
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

	success = true
	return entities, nil
}

// GetRecentEntities retrieves recently created entities
func (dm *DBManager) GetRecentEntities(ctx context.Context, projectName string, limit int) ([]apptype.Entity, error) {
	done := metrics.TimeOp("db_recent_entities")
	success := false
	defer func() { done(success) }()
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

	success = true
	return entities, nil
}

// CreateRelations creates multiple relations between entities
func (dm *DBManager) CreateRelations(ctx context.Context, projectName string, relations []apptype.Relation) error {
	done := metrics.TimeOp("db_create_relations")
	success := false
	defer func() { done(success) }()
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

	if err := tx.Commit(); err != nil {
		return err
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
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM observations WHERE entity_name = ?", name); err != nil {
			return fmt.Errorf("failed to delete observations for %q: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM relations WHERE source = ? OR target = ?", name, name); err != nil {
			return fmt.Errorf("failed to delete relations for %q: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM entities WHERE name = ?", name); err != nil {
			return fmt.Errorf("failed to delete entity %q: %w", name, err)
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
	if len(ids) > 0 {
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, 0, len(ids)+1)
		args = append(args, entityName)
		for _, id := range ids {
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
	if len(contents) > 0 {
		placeholders := strings.Repeat("?,", len(contents))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, 0, len(contents)+1)
		args = append(args, entityName)
		for _, c := range contents {
			args = append(args, c)
		}
		q := fmt.Sprintf("DELETE FROM observations WHERE entity_name = ? AND content IN (%s)", placeholders)
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("failed to delete observations by content: %w", err)
		}
		ra, _ := res.RowsAffected()
		total += ra
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	success = true
	return total, nil
}

// GetRelationsForEntities retrieves relations for a list of entities
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

// SearchNodes performs either vector or text search based on query type
func (dm *DBManager) SearchNodes(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	// If a strategy is set, delegate. Otherwise fall back to built-in logic below.
	if dm.search != nil {
		entities, relations, err := dm.search.Search(ctx, projectName, query, limit, offset)
		if err == nil {
			return entities, relations, nil
		}
		// Fall through to internal path on strategy error
		log.Printf("search strategy error, falling back: %v", err)
	}
	var entities []apptype.Entity
	var err error

	switch q := query.(type) {
	case []float32:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, q, limit, offset)
		if searchErr != nil {
			return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
		}
		entities = make([]apptype.Entity, len(results))
		for i, result := range results {
			entities[i] = result.Entity
		}
	case []float64:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		// Convert []float64 -> []float32
		vec := make([]float32, len(q))
		for i, v := range q {
			vec[i] = float32(v)
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, vec, limit, offset)
		if searchErr != nil {
			return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
		}
		entities = make([]apptype.Entity, len(results))
		for i, result := range results {
			entities[i] = result.Entity
		}
	case []interface{}:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		vec := make([]float32, len(q))
		for i, v := range q {
			switch n := v.(type) {
			case float64:
				vec[i] = float32(n)
			case float32:
				vec[i] = n
			case int:
				vec[i] = float32(n)
			case int64:
				vec[i] = float32(n)
			case json.Number:
				f, convErr := n.Float64()
				if convErr != nil {
					return nil, nil, fmt.Errorf("invalid vector element at index %d: %v", i, convErr)
				}
				vec[i] = float32(f)
			case string:
				f, convErr := strconv.ParseFloat(n, 64)
				if convErr != nil {
					return nil, nil, fmt.Errorf("invalid numeric string at index %d: %v", i, convErr)
				}
				vec[i] = float32(f)
			default:
				return nil, nil, fmt.Errorf("unsupported vector element type at index %d: %T", i, v)
			}
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, vec, limit, offset)
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
		entities, err = dm.SearchEntities(ctx, projectName, q, limit, offset)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to perform entity search: %w", err)
		}
	default:
		// Attempt to coerce unknown array-like types into a vector
		if coerced, ok, cerr := coerceToFloat32Slice(query); ok {
			if len(coerced) == 0 {
				return nil, nil, fmt.Errorf("vector query cannot be empty")
			}
			results, searchErr := dm.SearchSimilar(ctx, projectName, coerced, limit, offset)
			if searchErr != nil {
				return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
			}
			entities = make([]apptype.Entity, len(results))
			for i, result := range results {
				entities[i] = result.Entity
			}
			// proceed to relation fetch below
		} else if cerr != nil {
			return nil, nil, fmt.Errorf("invalid vector query: %v", cerr)
		}
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

// searchNodesInternal retains the pre-strategy behavior to ensure backward compatibility
func (dm *DBManager) searchNodesInternal(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	var entities []apptype.Entity
	var err error
	switch q := query.(type) {
	case []float32:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, q, limit, offset)
		if searchErr != nil {
			return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
		}
		entities = make([]apptype.Entity, len(results))
		for i, result := range results {
			entities[i] = result.Entity
		}
	case []float64:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		vec := make([]float32, len(q))
		for i, v := range q {
			vec[i] = float32(v)
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, vec, limit, offset)
		if searchErr != nil {
			return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
		}
		entities = make([]apptype.Entity, len(results))
		for i, result := range results {
			entities[i] = result.Entity
		}
	case []interface{}:
		if len(q) == 0 {
			return nil, nil, fmt.Errorf("vector query cannot be empty")
		}
		vec := make([]float32, len(q))
		for i, v := range q {
			switch n := v.(type) {
			case float64:
				vec[i] = float32(n)
			case float32:
				vec[i] = n
			case int:
				vec[i] = float32(n)
			case int64:
				vec[i] = float32(n)
			case json.Number:
				f, convErr := n.Float64()
				if convErr != nil {
					return nil, nil, fmt.Errorf("invalid vector element at index %d: %v", i, convErr)
				}
				vec[i] = float32(f)
			case string:
				f, convErr := strconv.ParseFloat(n, 64)
				if convErr != nil {
					return nil, nil, fmt.Errorf("invalid numeric string at index %d: %v", i, convErr)
				}
				vec[i] = float32(f)
			default:
				return nil, nil, fmt.Errorf("unsupported vector element type at index %d: %T", i, v)
			}
		}
		results, searchErr := dm.SearchSimilar(ctx, projectName, vec, limit, offset)
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
		entities, err = dm.SearchEntities(ctx, projectName, q, limit, offset)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to perform entity search: %w", err)
		}
	default:
		if coerced, ok, cerr := coerceToFloat32Slice(query); ok {
			if len(coerced) == 0 {
				return nil, nil, fmt.Errorf("vector query cannot be empty")
			}
			results, searchErr := dm.SearchSimilar(ctx, projectName, coerced, limit, offset)
			if searchErr != nil {
				return nil, nil, fmt.Errorf("failed to perform similarity search: %w", searchErr)
			}
			entities = make([]apptype.Entity, len(results))
			for i, result := range results {
				entities[i] = result.Entity
			}
		} else if cerr != nil {
			return nil, nil, fmt.Errorf("invalid vector query: %v", cerr)
		} else {
			return nil, nil, fmt.Errorf("unsupported query type: %T", query)
		}
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

// coerceToFloat32Slice attempts to interpret arbitrary slice-like inputs as a []float32
func coerceToFloat32Slice(value interface{}) ([]float32, bool, error) {
	switch v := value.(type) {
	case []float32:
		out := make([]float32, len(v))
		copy(out, v)
		return out, true, nil
	case []float64:
		out := make([]float32, len(v))
		for i, n := range v {
			out[i] = float32(n)
		}
		return out, true, nil
	case []int:
		out := make([]float32, len(v))
		for i, n := range v {
			out[i] = float32(n)
		}
		return out, true, nil
	case []int64:
		out := make([]float32, len(v))
		for i, n := range v {
			out[i] = float32(n)
		}
		return out, true, nil
	case []interface{}:
		out := make([]float32, len(v))
		for i, elem := range v {
			switch n := elem.(type) {
			case float64:
				out[i] = float32(n)
			case float32:
				out[i] = n
			case int:
				out[i] = float32(n)
			case int64:
				out[i] = float32(n)
			case json.Number:
				f, err := n.Float64()
				if err != nil {
					return nil, false, fmt.Errorf("invalid json.Number at index %d: %v", i, err)
				}
				out[i] = float32(f)
			case string:
				f, err := strconv.ParseFloat(n, 64)
				if err != nil {
					return nil, false, fmt.Errorf("invalid numeric string at index %d: %v", i, err)
				}
				out[i] = float32(f)
			default:
				return nil, false, fmt.Errorf("unsupported vector element type at index %d: %T", i, elem)
			}
		}
		return out, true, nil
	}

	// Try reflection for other slice/array kinds
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		n := rv.Len()
		out := make([]float32, n)
		for i := 0; i < n; i++ {
			el := rv.Index(i).Interface()
			switch x := el.(type) {
			case float64:
				out[i] = float32(x)
			case float32:
				out[i] = x
			case int:
				out[i] = float32(x)
			case int64:
				out[i] = float32(x)
			case json.Number:
				f, err := x.Float64()
				if err != nil {
					return nil, false, fmt.Errorf("invalid json.Number at index %d: %v", i, err)
				}
				out[i] = float32(f)
			case string:
				f, err := strconv.ParseFloat(x, 64)
				if err != nil {
					return nil, false, fmt.Errorf("invalid numeric string at index %d: %v", i, err)
				}
				out[i] = float32(f)
			default:
				return nil, false, fmt.Errorf("unsupported element type at index %d: %T", i, el)
			}
		}
		return out, true, nil
	}

	return nil, false, nil
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
