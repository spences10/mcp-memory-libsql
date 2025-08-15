package database

import (
    "context"
    "database/sql"
    "time"
    "strings"
)

// capFlags stores capability detection for a specific project/DB handle
type capFlags struct {
    checked    bool
    vectorTopK bool
    fts5       bool
}

// detectCapabilities probes presence of vector_top_k and records flags.
func (dm *DBManager) detectCapabilitiesForProject(ctx context.Context, projectName string, db *sql.DB) {
    dm.capMu.RLock()
    caps, ok := dm.capsByProject[projectName]
    dm.capMu.RUnlock()
    if ok && caps.checked {
        return
    }

    // Skip ANN probe for in-memory test URLs to avoid driver quirks
    if strings.Contains(dm.config.URL, "mode=memory") {
        dm.capMu.Lock()
        dm.capsByProject[projectName] = capFlags{checked: true, vectorTopK: false, fts5: caps.fts5}
        dm.capMu.Unlock()
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
    caps.vectorTopK = (err == nil)
    caps.checked = true

    // Detect FTS5 support by attempting to create a temporary virtual table
    ctx3, cancel3 := context.WithTimeout(ctx, 500*time.Millisecond)
    defer cancel3()
    if _, err := db.ExecContext(ctx3, "CREATE VIRTUAL TABLE IF NOT EXISTS temp._fts5_probe USING fts5(x)"); err == nil {
        // Clean up probe table
        _, _ = db.ExecContext(ctx3, "DROP TABLE IF EXISTS temp._fts5_probe")
        caps.fts5 = true
        // Ensure FTS schema/triggers exist for observations
        _ = dm.ensureFTSSchema(context.Background(), db)
        // Verify FTS table exists; if not, disable FTS capability
        if _, verr := db.ExecContext(context.Background(), "SELECT 1 FROM fts_observations WHERE 1=0"); verr != nil {
            caps.fts5 = false
        }
    } else {
        caps.fts5 = false
    }
    dm.capMu.Lock()
    dm.capsByProject[projectName] = caps
    dm.capMu.Unlock()
}


