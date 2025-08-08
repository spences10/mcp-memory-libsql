package database

import (
	"context"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
	emb "github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/embeddings"
)

const benchProject = "default"

func setupBenchDB(b *testing.B, n int) (*DBManager, func()) {
	b.Helper()
	cfg := NewConfig()
	cfg.URL = "file:benchdb?mode=memory&cache=shared"
	cfg.EmbeddingDims = 4
	dbm, err := NewDBManager(cfg)
	if err != nil {
		b.Fatalf("NewDBManager: %v", err)
	}

	// Seed data
	ctx := context.Background()
	rand.New(rand.NewSource(42))
	batch := make([]apptype.Entity, 0, 200)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := dbm.CreateEntities(ctx, benchProject, batch); err != nil {
			b.Fatalf("CreateEntities: %v", err)
		}
		batch = batch[:0]
	}
	for i := range n {
		name := fmtName(i)
		emb := make([]float32, cfg.EmbeddingDims)
		for d := 0; d < cfg.EmbeddingDims; d++ {
			emb[d] = rand.Float32()
		}
		obs := []string{
			"lorem ipsum",
			"dolor sit amet",
			"bench data",
		}
		batch = append(batch, apptype.Entity{Name: name, EntityType: "t", Observations: obs, Embedding: emb})
		if len(batch) == cap(batch) {
			flush()
		}
	}
	flush()

	cleanup := func() { _ = dbm.Close() }
	return dbm, cleanup
}

func fmtName(i int) string { return "e_" + strconv.Itoa(i) }

func BenchmarkSearchSimilar(b *testing.B) {
	dbm, cleanup := setupBenchDB(b, 2000)
	defer cleanup()

	ctx := context.Background()
	// Random query vector
	q := []float32{0.1, 0.2, 0.3, 0.4}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dbm.SearchSimilar(ctx, benchProject, q, 10, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchEntities_Text(b *testing.B) {
	dbm, cleanup := setupBenchDB(b, 2000)
	defer cleanup()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dbm.SearchEntities(ctx, benchProject, "lorem", 10, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchNodes_Hybrid(b *testing.B) {
	// Enable hybrid mode and set a mock provider
	_ = os.Setenv("HYBRID_SEARCH", "1")
	dbm, cleanup := setupBenchDB(b, 2000)
	defer cleanup()
	dbm.SetEmbeddingsProvider(&emb.StaticProvider{N: 4})
	// Swap strategy to hybrid explicitly
	dbm.search = newHybridSearchStrategy(dbm)

	ctx := context.Background()
	// Text query triggers hybrid fusion
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := dbm.SearchNodes(ctx, benchProject, "bench", 10, 0); err != nil {
			b.Fatal(err)
		}
	}
}
