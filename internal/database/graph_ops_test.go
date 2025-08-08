package database

import (
	"context"
	"testing"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNeighbors_Walk_ShortestPath(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Build small graph: a->b->c, a->d
	err := db.CreateEntities(ctx, testProject, []apptype.Entity{
		{Name: "a", EntityType: "t", Observations: []string{"oa"}},
		{Name: "b", EntityType: "t", Observations: []string{"ob"}},
		{Name: "c", EntityType: "t", Observations: []string{"oc"}},
		{Name: "d", EntityType: "t", Observations: []string{"od"}},
	})
	require.NoError(t, err)
	err = db.CreateRelations(ctx, testProject, []apptype.Relation{
		{From: "a", To: "b", RelationType: "r"},
		{From: "b", To: "c", RelationType: "r"},
		{From: "a", To: "d", RelationType: "r"},
	})
	require.NoError(t, err)

	// Neighbors out from a should include a, b, d and relation a->b, a->d
	ents, rels, err := db.GetNeighbors(ctx, testProject, []string{"a"}, "out", 0)
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, e := range ents {
		names[e.Name] = true
	}
	assert.True(t, names["a"])
	assert.True(t, names["b"])
	assert.True(t, names["d"])
	// Walk depth 2 from a should reach c as well
	wents, wrels, err := db.Walk(ctx, testProject, []string{"a"}, 2, "out", 0)
	require.NoError(t, err)
	wnames := make(map[string]bool)
	for _, e := range wents {
		wnames[e.Name] = true
	}
	assert.True(t, wnames["c"])
	assert.GreaterOrEqual(t, len(wrels), len(rels))

	// Shortest path a->c should yield 3 nodes and 2 edges
	pents, prels, err := db.ShortestPath(ctx, testProject, "a", "c", "out")
	require.NoError(t, err)
	assert.Len(t, pents, 3)
	assert.Len(t, prels, 2)
	assert.Equal(t, "a", prels[0].From)
	assert.Equal(t, "b", prels[0].To)
	assert.Equal(t, "b", prels[1].From)
	assert.Equal(t, "c", prels[1].To)
}
