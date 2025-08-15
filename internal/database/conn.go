package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/tursodatabase/go-libsql"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/embeddings"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

const defaultProject = "default"

// NewDBManager creates a new database manager
func NewDBManager(config *Config) (*DBManager, error) {
	if config.EmbeddingDims <= 0 || config.EmbeddingDims > 65536 {
		return nil, fmt.Errorf("{\"error\":{\"code\":\"INVALID_EMBEDDING_DIMS\",\"message\":\"EMBEDDING_DIMS must be between 1 and 65536 inclusive\",\"value\":%d}}", config.EmbeddingDims)
	}
	manager := &DBManager{
		config:        config,
		dbs:           make(map[string]*sql.DB),
		stmtCache:     make(map[string]map[string]*sql.Stmt),
		capsByProject: make(map[string]capFlags),
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
	// After schema/init, reconcile embedding dims with existing DB to avoid env drift.
	if dbDims := detectDBEmbeddingDims(newDb); dbDims > 0 && dbDims != dm.config.EmbeddingDims {
		log.Printf("Embedding dims mismatch: DB=%d, Config=%d. Adopting DB dims to preserve compatibility.", dbDims, dm.config.EmbeddingDims)
		dm.config.EmbeddingDims = dbDims
		// Re-wrap provider to match DB dims (pad/truncate policy via env)
		if dm.provider != nil && dm.provider.Dimensions() != dbDims {
			mode := os.Getenv("EMBEDDINGS_ADAPT_MODE")
			dm.provider = embeddings.WrapToDims(dm.provider, dbDims, mode)
		}
	}

	// Detect optional capabilities for this project DB handle
	dm.detectCapabilitiesForProject(context.Background(), projectName, newDb)
	// Observe initial pool stats
	stats := newDb.Stats()
	metrics.Default().ObservePoolStats(stats.InUse, stats.Idle)
	return newDb, nil
}

// detectDBEmbeddingDims introspects the schema to infer the F32_BLOB size for entities.embedding
func detectDBEmbeddingDims(db *sql.DB) int {
	// Attempt to read SQL DDL from sqlite_master
	// Fallback to PRAGMA table_info to estimate size from sample row if needed
	// Approach 1: read CREATE TABLE statement and parse F32_BLOB(n)
	var sqlText string
	_ = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='entities'").Scan(&sqlText)
	if sqlText != "" {
		low := strings.ToLower(sqlText)
		// find substring f32_blob(
		idx := strings.Index(low, "f32_blob(")
		if idx >= 0 {
			rest := low[idx+len("f32_blob("):]
			end := strings.Index(rest, ")")
			if end > 0 {
				num := strings.TrimSpace(rest[:end])
				if n, err := strconv.Atoi(num); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	// Approach 2: try a sample read and infer length/4
	var blob []byte
	_ = db.QueryRow("SELECT embedding FROM entities LIMIT 1").Scan(&blob)
	if len(blob) > 0 && len(blob)%4 == 0 {
		return len(blob) / 4
	}
	return 0
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
