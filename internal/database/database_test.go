package database

import (
	"context"
	"os"
	"testing"

	apptype "github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
)

func TestDBManager(t *testing.T) {
	// Create a temporary database for testing
	tempDir, err := os.MkdirTemp("", "libsql-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := tempDir + "/test.db"
	config := &Config{
		URL: "file:" + dbPath,
	}

	// Create database manager
	db, err := NewDBManager(config)
	if err != nil {
		t.Fatalf("Failed to create database manager: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Test basic operations
	t.Run("CreateAndRetrieveEntity", func(t *testing.T) {
		// Create a test entity
		entities := []apptype.Entity{
			{
				Name:         "test-entity",
				EntityType:   "test-type",
				Observations: []string{"This is a test observation"},
				Embedding:    []float32{0.1, 0.2, 0.3, 0.4},
			},
		}

		if err := db.CreateEntities(ctx, entities); err != nil {
			t.Fatalf("Failed to create entities: %v", err)
		}

		// Retrieve the entity
		entity, err := db.GetEntity(ctx, "test-entity")
		if err != nil {
			t.Fatalf("Failed to get entity: %v", err)
		}

		if entity.Name != "test-entity" {
			t.Errorf("Expected entity name 'test-entity', got '%s'", entity.Name)
		}

		if entity.EntityType != "test-type" {
			t.Errorf("Expected entity type 'test-type', got '%s'", entity.EntityType)
		}

		if len(entity.Observations) != 1 {
			t.Errorf("Expected 1 observation, got %d", len(entity.Observations))
		}

		if len(entity.Embedding) != 4 {
			t.Errorf("Expected embedding with 4 dimensions, got %d", len(entity.Embedding))
		}
	})

	t.Run("CreateRelations", func(t *testing.T) {
		// First create the target entity
		entities := []apptype.Entity{
			{
				Name:         "another-entity",
				EntityType:   "test-type",
				Observations: []string{"This is another test observation"},
				Embedding:    []float32{0.5, 0.6, 0.7, 0.8},
			},
		}

		if err := db.CreateEntities(ctx, entities); err != nil {
			t.Fatalf("Failed to create target entity: %v", err)
		}

		// Create test relations
		relations := []apptype.Relation{
			{
				From:         "test-entity",
				To:           "another-entity",
				RelationType: "related-to",
			},
		}

		if err := db.CreateRelations(ctx, relations); err != nil {
			t.Fatalf("Failed to create relations: %v", err)
		}
	})

	t.Run("SearchNodes", func(t *testing.T) {
		// Test text search
		entities, _, err := db.SearchNodes(ctx, "test")
		if err != nil {
			t.Fatalf("Failed to search nodes: %v", err)
		}

		if len(entities) == 0 {
			t.Error("Expected at least one entity from search")
		}

		// Test vector search
		vector := []float32{0.1, 0.2, 0.3, 0.4}
		entities, _, err = db.SearchNodes(ctx, vector)
		if err != nil {
			t.Fatalf("Failed to search nodes with vector: %v", err)
		}

		if len(entities) == 0 {
			t.Error("Expected at least one entity from vector search")
		}
	})

	t.Run("ReadGraph", func(t *testing.T) {
		// Test read graph
		entities, relations, err := db.ReadGraph(ctx)
		if err != nil {
			t.Fatalf("Failed to read graph: %v", err)
		}

		if len(entities) == 0 {
			t.Error("Expected at least one entity from read graph")
		}

		// Relations might be empty depending on test order
		_ = relations
	})
}
