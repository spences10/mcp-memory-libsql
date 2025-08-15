package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	start := min(offset, len(scoredList))
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

// const defaultProject now defined in conn.go

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

// buildFTSMatchExpr builds a robust MATCH expression for FTS5 that:
//   - treats trailing '*' as prefix operator
//   - if the query contains a single token with a trailing ':*' pattern (e.g., "Task:*"),
//     it rewrites to search both columns for tokens starting with "Task:" using prefix
//   - otherwise returns the raw query
func (dm *DBManager) buildFTSMatchExpr(raw string) string {
	q := strings.TrimSpace(raw)
	if q == "" {
		return q
	}
	// If looks like Term:* (single token ending with :*)
	if !strings.ContainsAny(q, " \t\n\r\f\v\u00A0") && strings.HasSuffix(q, ":*") {
		base := strings.TrimSuffix(q, ":*")
		base = strings.TrimSpace(base)
		if base != "" {
			// Use column-qualified prefix queries on both columns
			// Quote the token to avoid column lookups (unicode61 tokenchars allows ':')
			// Example: entity_name:"Task:"* OR content:"Task:"*
			return fmt.Sprintf("entity_name:\"%s:\"* OR content:\"%s:\"*", base, base)
		}
	}
	// If plain token with '*' suffix, let FTS handle as prefix
	return q
}

// capFlags moved to capabilities.go

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

// getPreparedStmt returns or prepares and caches a statement for the given project DB
// implemented in stmt_cache.go

// detectCapabilities probes presence of vector_top_k and records flags.
// NOTE: implementation lives in capabilities.go

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
		// Compute new endpoints and relation type; trim inputs
		nf := strings.TrimSpace(up.NewFrom)
		if nf == "" {
			nf = strings.TrimSpace(up.From)
		}
		nt := strings.TrimSpace(up.NewTo)
		if nt == "" {
			nt = strings.TrimSpace(up.To)
		}
		nr := strings.TrimSpace(up.NewRelationType)
		if nr == "" {
			nr = strings.TrimSpace(up.RelationType)
		}
		if nf == "" || nt == "" || nr == "" {
			return fmt.Errorf("relation endpoints and type cannot be empty")
		}

		// Pre-check that both endpoints exist to avoid FK failures; clearer error message
		rows, qerr := tx.QueryContext(ctx, "SELECT name FROM entities WHERE name IN (?, ?)", nf, nt)
		if qerr != nil {
			return fmt.Errorf("failed to verify relation endpoints: %w", qerr)
		}
		found := make(map[string]bool, 2)
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				found[name] = true
			}
		}
		rows.Close()
		missing := make([]string, 0, 2)
		if !found[nf] {
			missing = append(missing, nf)
		}
		if !found[nt] {
			missing = append(missing, nt)
		}
		if len(missing) > 0 {
			return fmt.Errorf("relation endpoints must exist before linking: missing %s", strings.Join(missing, ", "))
		}

		// delete old tuple
		if _, err := tx.ExecContext(ctx, "DELETE FROM relations WHERE source = ? AND target = ? AND relation_type = ?", up.From, up.To, up.RelationType); err != nil {
			return fmt.Errorf("failed to delete old relation: %w", err)
		}
		// insert new tuple
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
	dm.capMu.RLock()
	caps := dm.capsByProject[projectName]
	dm.capMu.RUnlock()
	useTopK := caps.vectorTopK

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
			dm.capMu.Lock()
			c := dm.capsByProject[projectName]
			c.vectorTopK = false
			dm.capsByProject[projectName] = c
			dm.capMu.Unlock()
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

	// Prepare LIKE pattern and normalize simple wildcards: treat '*' as SQL '%'
	likePattern := "%" + strings.ReplaceAll(query, "*", "%") + "%"
	if limit <= 0 {
		limit = 5
	}
	if offset < 0 {
		offset = 0
	}
	// Prefer FTS5 if available
	dm.capMu.RLock()
	caps := dm.capsByProject[projectName]
	dm.capMu.RUnlock()
	useFTS := caps.fts5
	var rows *sql.Rows
	if useFTS {
		// Prefer BM25 ranking if available or enabled; fallback to simple name ordering
		bm25Enabled := true
		if v := os.Getenv("BM25_ENABLE"); strings.EqualFold(v, "false") || v == "0" {
			bm25Enabled = false
		}
		bmK1 := os.Getenv("BM25_K1")
		bmB := os.Getenv("BM25_B")
		bmExpr := "bm25(f)"
		if bm25Enabled && bmK1 != "" && bmB != "" {
			bmExpr = fmt.Sprintf("bm25(f,%s,%s)", bmK1, bmB)
		}
		qftsBase := "SELECT DISTINCT e.name, e.entity_type, e.embedding\n" +
			"            FROM fts_observations f\n" +
			"            JOIN observations o ON o.id = f.rowid\n" +
			"            JOIN entities e ON e.name = o.entity_name\n" +
			"            WHERE f.fts_observations MATCH ?\n"
		qftsOrderBM := fmt.Sprintf("%s            ORDER BY %s ASC\n            LIMIT ? OFFSET ?", qftsBase, bmExpr)
		qftsOrderName := qftsBase + "            ORDER BY e.name ASC\n            LIMIT ? OFFSET ?"

		// Build a tolerant FTS expression that accommodates common syntaxes like "Task:*"
		expr := dm.buildFTSMatchExpr(query)

		// Try BM25 first if enabled
		var err error
		if bm25Enabled {
			if stmt, perr := dm.getPreparedStmt(ctx, projectName, db, qftsOrderBM); perr == nil {
				rows, err = stmt.QueryContext(ctx, expr, limit, offset)
			} else {
				err = perr
			}
			if err != nil {
				low := strings.ToLower(err.Error())
				if strings.Contains(low, "no such function: bm25") || strings.Contains(low, "wrong number of arguments to function bm25") {
					// Fall back to name ordering below
					err = nil
				} else if strings.Contains(low, "no such module: fts5") {
					dm.capMu.Lock()
					c := dm.capsByProject[projectName]
					c.fts5 = false
					dm.capsByProject[projectName] = c
					dm.capMu.Unlock()
					useFTS = false
				} else if strings.Contains(low, "malformed match") || strings.Contains(low, "no such column") || strings.Contains(low, "no such table: fts_observations") {
					useFTS = false
				} else {
					return nil, fmt.Errorf("failed to execute FTS search: %w", err)
				}
			}
		}
		// If we didn't obtain rows via BM25 (disabled or fell through), try name ordering
		if useFTS && rows == nil {
			stmt, perr := dm.getPreparedStmt(ctx, projectName, db, qftsOrderName)
			if perr != nil {
				return nil, perr
			}
			rows, err = stmt.QueryContext(ctx, expr, limit, offset)
			if err != nil {
				low := strings.ToLower(err.Error())
				if strings.Contains(low, "no such module: fts5") {
					dm.capMu.Lock()
					c := dm.capsByProject[projectName]
					c.fts5 = false
					dm.capsByProject[projectName] = c
					dm.capMu.Unlock()
					useFTS = false
				} else if strings.Contains(low, "malformed match") || strings.Contains(low, "no such column") || strings.Contains(low, "no such table: fts_observations") {
					useFTS = false
				} else {
					return nil, fmt.Errorf("failed to execute FTS search: %w", err)
				}
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
		rows, err = stmt.QueryContext(ctx, likePattern, likePattern, likePattern, limit, offset)
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
// moved to vectors.go

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
