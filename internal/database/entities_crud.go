package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/metrics"
)

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
