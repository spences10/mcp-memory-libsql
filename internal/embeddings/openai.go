package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
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
	return &openAIProvider{model: model, dims: dims, http: &http.Client{Timeout: 15 * time.Second}, apiKey: apiKey}
}

func (p *openAIProvider) Name() string    { return "openai" }
func (p *openAIProvider) Dimensions() int { return p.dims }

func (p *openAIProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	// OpenAI Embeddings API: https://api.openai.com/v1/embeddings
	// Request: {"model": ..., "input": ["..."]}
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	payload := map[string]interface{}{
		"model": p.model,
		"input": inputs,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var b struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		if b.Error.Message != "" {
			return nil, fmt.Errorf("openai embeddings error: %s", b.Error.Message)
		}
		return nil, fmt.Errorf("openai embeddings http status: %s", resp.Status)
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	res := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		res = append(res, f64to32(d.Embedding))
	}
	return res, nil
}

func f64to32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i := range v {
		out[i] = float32(v[i])
	}
	return out
}
