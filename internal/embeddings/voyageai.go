package embeddings

import (
	"context"
	"os"
	"strconv"
	"strings"

	voyageai "github.com/austinfhunter/voyageai"
)

// Voyage AI embeddings provider using github.com/austinfhunter/voyageai
// Docs: https://pkg.go.dev/github.com/austinfhunter/voyageai

type voyageProvider struct {
	client *voyageai.VoyageClient
	model  string
	dims   int
}

func newVoyageFromEnv() Provider {
	// API key is required. Support VOYAGEAI_API_KEY and VOYAGE_API_KEY aliases.
	key := strings.TrimSpace(os.Getenv("VOYAGEAI_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("VOYAGE_API_KEY"))
		if key == "" {
			return nil
		}
	}
	model := strings.TrimSpace(os.Getenv("VOYAGEAI_EMBEDDINGS_MODEL"))
	if model == "" {
		// default recommended model
		model = "voyage-3-lite"
	}

	// Establish dimensions. Prefer explicit env override, else try global EMBEDDING_DIMS to stay in sync with DB schema.
	dims := 1024
	if v := strings.TrimSpace(os.Getenv("VOYAGEAI_EMBEDDINGS_DIMS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dims = n
		}
	} else if v := strings.TrimSpace(os.Getenv("EMBEDDING_DIMS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dims = n
		}
	}

	client := voyageai.NewClient(&voyageai.VoyageClientOpts{Key: key})
	return &voyageProvider{client: client, model: model, dims: dims}
}

func (p *voyageProvider) Name() string    { return "voyageai" }
func (p *voyageProvider) Dimensions() int { return p.dims }

func (p *voyageProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	// Use client.Embed to batch request
	resp, err := p.client.Embed(inputs, p.model, nil)
	if err != nil {
		return nil, err
	}
	// The SDK returns []float32 already
	out := make([][]float32, 0, len(resp.Data))
	for _, item := range resp.Data {
		out = append(out, item.Embedding)
	}
	return out, nil
}
