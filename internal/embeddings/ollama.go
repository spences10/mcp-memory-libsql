package embeddings

import (
	"context"
	"errors"
	"net/http"
	"os"
)

type ollamaProvider struct {
	host  string
	model string
	dims  int
	http  *http.Client
}

func newOllamaFromEnv() Provider {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		return nil
	}
	model := os.Getenv("OLLAMA_EMBEDDINGS_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	dims := 768
	return &ollamaProvider{host: host, model: model, dims: dims, http: &http.Client{}}
}

func (p *ollamaProvider) Name() string    { return "ollama" }
func (p *ollamaProvider) Dimensions() int { return p.dims }
func (p *ollamaProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	return nil, errors.New("ollama embeddings not implemented")
}
