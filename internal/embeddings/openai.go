package embeddings

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
)

// Minimal stub; call sites should handle nil provider gracefully.
type openAIProvider struct {
	model  string
	dims   int
	http   *http.Client
	apiKey string
}

func newOpenAIFromEnv() Provider {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil
	}
	model := os.Getenv("OPENAI_EMBEDDINGS_MODEL")
	if model == "" {
		model = "text-embedding-3-small"
	}
	dims := 1536
	if strings.Contains(model, "small") {
		dims = 1536
	} else if strings.Contains(model, "large") {
		dims = 3072
	}
	return &openAIProvider{model: model, dims: dims, http: &http.Client{}, apiKey: apiKey}
}

func (p *openAIProvider) Name() string    { return "openai" }
func (p *openAIProvider) Dimensions() int { return p.dims }

func (p *openAIProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	// Intentionally left as a stub to avoid bringing in extra deps at this step.
	// Future: implement HTTP call to OpenAI embeddings endpoint.
	return nil, errors.New("openai embeddings not implemented")
}
