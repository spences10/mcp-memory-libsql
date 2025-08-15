package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

// getPreparedStmt returns or prepares and caches a statement for the given project DB
func (dm *DBManager) getPreparedStmt(ctx context.Context, projectName string, db *sql.DB, sqlText string) (*sql.Stmt, error) {
	// fast path read
	dm.stmtMu.RLock()
	if projCache, ok := dm.stmtCache[projectName]; ok {
		if stmt, ok2 := projCache[sqlText]; ok2 {
			dm.stmtMu.RUnlock()
			metrics.Default().IncStmtCacheHit("prepare")
			return stmt, nil
		}
	}
	dm.stmtMu.RUnlock()
	metrics.Default().IncStmtCacheMiss("prepare")

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


