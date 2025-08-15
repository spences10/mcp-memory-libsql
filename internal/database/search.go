package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

// SearchStrategy allows pluggable search implementations (text/vector/hybrid)
type SearchStrategy interface {
	Search(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error)
}

type defaultSearchStrategy struct{ dm *DBManager }

func (s *defaultSearchStrategy) Search(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error) {
	return s.dm.searchNodesInternal(ctx, projectName, query, limit, offset)
}

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
	qStr, ok := query.(string)
	if !ok || strings.TrimSpace(qStr) == "" {
		return s.dm.searchNodesInternal(ctx, projectName, query, limit, offset)
	}
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
	type scored struct {
		entity apptype.Entity
		score  float64
	}
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

// SearchNodes performs either vector or text search based on query type
func (dm *DBManager) SearchNodes(ctx context.Context, projectName string, query interface{}, limit int, offset int) ([]apptype.Entity, []apptype.Relation, error) {
    if dm.search != nil {
        entities, relations, err := dm.search.Search(ctx, projectName, query, limit, offset)
        if err == nil {
            return entities, relations, nil
        }
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
	likePattern := "%" + strings.ReplaceAll(query, "*", "%") + "%"
	if limit <= 0 {
		limit = 5
	}
	if offset < 0 {
		offset = 0
	}
	dm.capMu.RLock()
	caps := dm.capsByProject[projectName]
	dm.capMu.RUnlock()
	useFTS := caps.fts5
	var rows *sql.Rows
	if useFTS {
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
		qftsBase := "SELECT DISTINCT e.name, e.entity type, e.embedding\n" +
			"            FROM fts_observations f\n" +
			"            JOIN observations o ON o.id = f.rowid\n" +
			"            JOIN entities e ON e.name = o.entity_name\n" +
			"            WHERE f.fts_observations MATCH ?\n"
		qftsOrderBM := fmt.Sprintf("%s            ORDER BY %s ASC\n            LIMIT ? OFFSET ?", qftsBase, bmExpr)
		qftsOrderName := qftsBase + "            ORDER BY e.name ASC\n            LIMIT ? OFFSET ?"
		expr := dm.buildFTSMatchExpr(query)
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
