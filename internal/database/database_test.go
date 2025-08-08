package database

import (
	"context"
	"os"
	"testing"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/embeddings"
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
	// Ensure valid embedding dims to satisfy guard
	config.EmbeddingDims = 4
	// FIXME:  Ensure hybrid disabled by default in tests - we need to test it
	os.Setenv("HYBRID_SEARCH", "")
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
		EmbeddingDims:    4,
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

// setupFileDB creates a real file-backed database to validate non-memory behavior
func setupFileDB(t *testing.T) (*DBManager, string, func()) {
	dir, err := os.MkdirTemp("", "mcp-mem-filedb")
	require.NoError(t, err)
	dbPath := dir + "/libsql.db"
	cfg := &Config{URL: "file:" + dbPath, EmbeddingDims: 4}
	db, err := NewDBManager(cfg)
	require.NoError(t, err)
	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	}
	return db, dbPath, cleanup
}

func TestFileDB_CreateAndSearch(t *testing.T) {
	db, _, cleanup := setupFileDB(t)
	defer cleanup()

	ctx := context.Background()
	// Create a couple of entities
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "alpha", EntityType: "kind", Observations: []string{"first"}},
		{Name: "beta", EntityType: "kind", Observations: []string{"second"}},
	})
	require.NoError(t, err)

	// Text search path
	ents, _, err := db.SearchNodes(ctx, testProject, "alpha", 5, 0)
	require.NoError(t, err)
	require.Len(t, ents, 1)
	assert.Equal(t, "alpha", ents[0].Name)

	// Vector search path (fallback to exact if ANN unsupported)
	_, _, err = db.SearchNodes(ctx, testProject, []float32{0.1, 0.2, 0.3, 0.4}, 5, 0)
	require.NoError(t, err)
}

func TestHybridSearch_TextOnlyFallback(t *testing.T) {
	// Hybrid enabled but no provider/dims match -> should degrade to text-only
	os.Setenv("HYBRID_SEARCH", "true")
	defer os.Setenv("HYBRID_SEARCH", "")
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "hy-a", EntityType: "k", Observations: []string{"alpha"}},
		{Name: "hy-b", EntityType: "k", Observations: []string{"beta"}},
	}))
	ents, _, err := db.SearchNodes(ctx, testProject, "alpha", 5, 0)
	require.NoError(t, err)
	require.NotEmpty(t, ents)
}

func TestHybridSearch_RankingFusion(t *testing.T) {
	// Enable hybrid and install a static provider to ensure dims match and vector path engages
	os.Setenv("HYBRID_SEARCH", "true")
	defer os.Setenv("HYBRID_SEARCH", "")
	db, cleanup := setupTestDB(t)
	defer cleanup()
	// Override provider with static 4-dim
	db.SetEmbeddingsProvider(&embeddings.StaticProvider{N: 4})

	ctx := context.Background()
	require.NoError(t, db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "hx-a", EntityType: "k", Observations: []string{"alpha apple"}},
		{Name: "hx-b", EntityType: "k", Observations: []string{"alpha beta"}},
		{Name: "hx-c", EntityType: "k", Observations: []string{"gamma"}},
	}))

	ents, _, err := db.SearchNodes(ctx, testProject, "alpha", 5, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(ents), 2)
	// Expect both alpha-containing docs appear high; order may vary by scoring, but both should be top-2
	names := []string{}
	for i := 0; i < len(ents) && i < 2; i++ {
		names = append(names, ents[i].Name)
	}
	// assert hx-a or hx-b are in the first two
	assert.Condition(t, func() bool {
		foundA, foundB := false, false
		for _, n := range names {
			if n == "hx-a" {
				foundA = true
			}
			if n == "hx-b" {
				foundB = true
			}
		}
		return foundA || foundB
	}, "expected hx-a or hx-b in top-2 hybrid results")
}

func TestFileDB_MultiProject(t *testing.T) {
	dir, err := os.MkdirTemp("", "mcp-mem-filedb-mp")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	cfg := &Config{ProjectsDir: dir, MultiProjectMode: true, EmbeddingDims: 4}
	db, err := NewDBManager(cfg)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	// Write to P1 and P2
	require.NoError(t, db.CreateEntities(ctx, "P1", []apptype.Entity{{Name: "n1", EntityType: "t", Observations: []string{"o1"}}}))
	require.NoError(t, db.CreateEntities(ctx, "P2", []apptype.Entity{{Name: "n2", EntityType: "t", Observations: []string{"o2"}}}))

	// Read back
	e1, err := db.GetEntity(ctx, "P1", "n1")
	require.NoError(t, err)
	assert.Equal(t, "n1", e1.Name)
	_, err = db.GetEntity(ctx, "P1", "n2")
	assert.Error(t, err)

	e2, err := db.GetEntity(ctx, "P2", "n2")
	require.NoError(t, err)
	assert.Equal(t, "n2", e2.Name)
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

	// FIXME: This part of the test is limited because we don't have a direct GetRelations method
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
	results, _, err := db.SearchNodes(ctx, testProject, "apple", 5, 0)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "apple", results[0].Name)

	// Vector search path: ensure different JSON-like vector forms are accepted
	_, _, err = db.SearchNodes(ctx, testProject, []float32{0.1, 0.2, 0.3, 0.4}, 5, 0)
	require.NoError(t, err)
	_, _, err = db.SearchNodes(ctx, testProject, []float64{0.1, 0.2, 0.3, 0.4}, 5, 0)
	require.NoError(t, err)
	_, _, err = db.SearchNodes(ctx, testProject, []interface{}{0.1, 0.2, 0.3, 0.4}, 5, 0)
	require.NoError(t, err)
	_, _, err = db.SearchNodes(ctx, testProject, []interface{}{"0.1", "0.2", "0.3", "0.4"}, 5, 0)
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

func TestAddObservationsAndOpenNodes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	// Create entity
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "e1", EntityType: "t", Observations: []string{"o1"}},
	})
	require.NoError(t, err)

	// Add observations
	err = db.AddObservations(ctx, testProject, "e1", []string{"o2", "o3"})
	require.NoError(t, err)

	// Get entity and check observations grew
	e, err := db.GetEntity(ctx, testProject, "e1")
	require.NoError(t, err)
	assert.Len(t, e.Observations, 3)

	// Create another entity and test GetEntities (open_nodes backend)
	err = db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "e2", EntityType: "t", Observations: []string{"x"}},
	})
	require.NoError(t, err)

	list, err := db.GetEntities(ctx, testProject, []string{"e1", "e2"})
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestBulkDeleteAndObservationDeletes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	// Setup two entities and a relation
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "a", EntityType: "t", Observations: []string{"oa1", "oa2"}},
		{Name: "b", EntityType: "t", Observations: []string{"ob1"}},
	})
	require.NoError(t, err)

	err = db.CreateRelations(ctx, testProject, []apptype.Relation{{From: "a", To: "b", RelationType: "r"}})
	require.NoError(t, err)

	// Delete one observation by content
	ra, err := db.DeleteObservations(ctx, testProject, "a", nil, []string{"oa1"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ra, int64(1))
	e, err := db.GetEntity(ctx, testProject, "a")
	require.NoError(t, err)
	assert.NotContains(t, e.Observations, "oa1")

	// Delete relations in bulk
	err = db.DeleteRelations(ctx, testProject, []apptype.Relation{{From: "a", To: "b", RelationType: "r"}})
	require.NoError(t, err)

	// Verify no relations between a and b
	ents, err := db.GetEntities(ctx, testProject, []string{"a", "b"})
	require.NoError(t, err)
	rels, err := db.GetRelationsForEntities(ctx, testProject, ents)
	require.NoError(t, err)
	assert.Len(t, rels, 0)

	// Bulk delete entities
	err = db.DeleteEntities(ctx, testProject, []string{"a", "b"})
	require.NoError(t, err)
	_, err = db.GetEntity(ctx, testProject, "a")
	assert.Error(t, err)
	_, err = db.GetEntity(ctx, testProject, "b")
	assert.Error(t, err)
}

func TestUpdateEntitiesAndRelations(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	// Seed entities and relation
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "x", EntityType: "t1", Observations: []string{"ox1"}},
		{Name: "y", EntityType: "t1", Observations: []string{"oy1"}},
	})
	require.NoError(t, err)
	err = db.CreateRelations(ctx, testProject, []apptype.Relation{{From: "x", To: "y", RelationType: "r"}})
	require.NoError(t, err)

	// Update entity type and merge observation
	err = db.UpdateEntities(ctx, testProject, []apptype.UpdateEntitySpec{
		{Name: "x", EntityType: "t2", MergeObservations: []string{"ox2"}},
	})
	require.NoError(t, err)
	ex, err := db.GetEntity(ctx, testProject, "x")
	require.NoError(t, err)
	assert.Equal(t, "t2", ex.EntityType)
	assert.Contains(t, ex.Observations, "ox2")

	// Update relation tuple x->y to x->z
	err = db.CreateEntities(ctx, testProject, []apptype.Entity{{Name: "z", EntityType: "t1", Observations: []string{"oz1"}}})
	require.NoError(t, err)
	err = db.UpdateRelations(ctx, testProject, []apptype.UpdateRelationChange{{From: "x", To: "y", RelationType: "r", NewTo: "z"}})
	require.NoError(t, err)
	ents, err := db.GetEntities(ctx, testProject, []string{"x", "y", "z"})
	require.NoError(t, err)
	rels, err := db.GetRelationsForEntities(ctx, testProject, ents)
	require.NoError(t, err)
	// Ensure x->z exists and x->y no longer present
	foundXZ := false
	for _, r := range rels {
		if r.From == "x" && r.To == "z" {
			foundXZ = true
		}
		assert.False(t, r.From == "x" && r.To == "y")
	}
	assert.True(t, foundXZ)
}
