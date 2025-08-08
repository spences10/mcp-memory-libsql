package database

import (
	"context"
	"os"
	"testing"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProject = "test-project"

func setupTestDB(t *testing.T) (*DBManager, func()) {
	config := NewConfig()
	// Use an in-memory database for testing.
	// The `cache=shared` is crucial for sharing the connection across different
	// calls to `sql.Open` within the same process.
	config.URL = "file:testdb?mode=memory&cache=shared"
	db, err := NewDBManager(config)
	require.NoError(t, err)

	cleanup := func() {
		err := db.Close()
		assert.NoError(t, err)
	}

	return db, cleanup
}

func TestCreateAndGetEntity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	entity := apptype.Entity{
		Name:         "test-entity",
		EntityType:   "test-type",
		Observations: []string{"obs1", "obs2"},
	}

	err := db.CreateEntities(ctx, testProject, []apptype.Entity{entity})
	require.NoError(t, err)

	retrieved, err := db.GetEntity(ctx, testProject, "test-entity")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, "test-entity", retrieved.Name)
	assert.Equal(t, "test-type", retrieved.EntityType)
	assert.Equal(t, []string{"obs1", "obs2"}, retrieved.Observations)
}

func TestMultiProject(t *testing.T) {
	dir, err := os.MkdirTemp("", "mcp-mem-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	config := &Config{
		ProjectsDir:      dir,
		MultiProjectMode: true,
	}

	db, err := NewDBManager(config)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	entity1 := apptype.Entity{Name: "entity1", EntityType: "type1", Observations: []string{"obs1"}}
	entity2 := apptype.Entity{Name: "entity2", EntityType: "type2", Observations: []string{"obs2"}}

	// Create entities in different projects
	err = db.CreateEntities(ctx, "project1", []apptype.Entity{entity1})
	require.NoError(t, err)
	err = db.CreateEntities(ctx, "project2", []apptype.Entity{entity2})
	require.NoError(t, err)

	// Verify entities exist in their respective projects
	retrieved1, err := db.GetEntity(ctx, "project1", "entity1")
	require.NoError(t, err)
	assert.Equal(t, "entity1", retrieved1.Name)

	retrieved2, err := db.GetEntity(ctx, "project2", "entity2")
	require.NoError(t, err)
	assert.Equal(t, "entity2", retrieved2.Name)

	// Verify entity does not exist in the other project
	_, err = db.GetEntity(ctx, "project1", "entity2")
	assert.Error(t, err)
	_, err = db.GetEntity(ctx, "project2", "entity1")
	assert.Error(t, err)
}

func TestRelations(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	entities := []apptype.Entity{
		{Name: "source-entity", EntityType: "type", Observations: []string{"obs"}},
		{Name: "target-entity", EntityType: "type", Observations: []string{"obs"}},
	}
	err := db.CreateEntities(ctx, testProject, entities)
	require.NoError(t, err)

	relations := []apptype.Relation{
		{From: "source-entity", To: "target-entity", RelationType: "connects_to"},
	}
	err = db.CreateRelations(ctx, testProject, relations)
	require.NoError(t, err)

	// This part of the test is limited because we don't have a direct GetRelations method
	// We test that the relations are created without error.
	// A more complete test would involve a ReadGraph or similar method.
}

func TestSearch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	// Setup entities
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "apple", EntityType: "fruit", Observations: []string{"a red fruit"}},
		{Name: "banana", EntityType: "fruit", Observations: []string{"a yellow fruit"}},
	})
	require.NoError(t, err)

	// Text search
	results, _, err := db.SearchNodes(ctx, testProject, "apple")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "apple", results[0].Name)

	// Vector search path: ensure different JSON-like vector forms are accepted
	_, _, err = db.SearchNodes(ctx, testProject, []float32{0.1, 0.2, 0.3, 0.4})
	require.NoError(t, err)
	_, _, err = db.SearchNodes(ctx, testProject, []float64{0.1, 0.2, 0.3, 0.4})
	require.NoError(t, err)
	_, _, err = db.SearchNodes(ctx, testProject, []interface{}{0.1, 0.2, 0.3, 0.4})
	require.NoError(t, err)
	_, _, err = db.SearchNodes(ctx, testProject, []interface{}{"0.1", "0.2", "0.3", "0.4"})
	require.NoError(t, err)
}

func TestReadGraph(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "entity1", EntityType: "type1", Observations: []string{"obs1"}},
	})
	require.NoError(t, err)

	entities, relations, err := db.ReadGraph(ctx, testProject, 10)
	require.NoError(t, err)
	assert.NotEmpty(t, entities)
	// Relations might be empty if none were created
	assert.NotNil(t, relations)
}
