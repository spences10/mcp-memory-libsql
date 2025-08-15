package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

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

		if _, err := tx.ExecContext(ctx, "DELETE FROM relations WHERE source = ? AND target = ? AND relation_type = ?", up.From, up.To, up.RelationType); err != nil {
			return fmt.Errorf("failed to delete old relation: %w", err)
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
