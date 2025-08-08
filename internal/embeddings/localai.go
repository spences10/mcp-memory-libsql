package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// LocalAI OpenAI-compatible /v1/embeddings
// BASE_URL default http://localhost:8080/v1

type localAIProvider struct {
	baseURL string // e.g., http://localhost:8080/v1
	model   string
	dims    int
	http    *http.Client
	apiKey  string // optional
}

func newLocalAIFromEnv() Provider {
	base := strings.TrimSpace(os.Getenv("LOCALAI_BASE_URL"))
	if base == "" {
		base = "http://localhost:8080/v1"
	}
	model := strings.TrimSpace(os.Getenv("LOCALAI_EMBEDDINGS_MODEL"))
	if model == "" {
		model = "text-embedding-ada-002"
	}
	dims := 1536
	if strings.Contains(model, "large") {
		dims = 3072
	}
	return &localAIProvider{baseURL: base, model: model, dims: dims, http: &http.Client{Timeout: 15 * time.Second}, apiKey: os.Getenv("LOCALAI_API_KEY")}
}

func (p *localAIProvider) Name() string    { return "localai" }
func (p *localAIProvider) Dimensions() int { return p.dims }

func (p *localAIProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	base, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, err
	}
	embURL := *base
	embURL.Path = path.Join(embURL.Path, "/embeddings")
	payload := map[string]any{
		"model": p.model,
		"input": inputs,
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embURL.String(), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var er struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&er)
		if er.Error.Message != "" {
			return nil, fmt.Errorf("localai error: %s", er.Error.Message)
		}
		return nil, fmt.Errorf("localai http status: %s", resp.Status)
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
