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
	"time"
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
	return &ollamaProvider{host: host, model: model, dims: dims, http: &http.Client{Timeout: 15 * time.Second}}
}

func (p *ollamaProvider) Name() string    { return "ollama" }
func (p *ollamaProvider) Dimensions() int { return p.dims }
func (p *ollamaProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	// Prefer new /api/embed (v0.2.6+); fall back to /api/embeddings
	reqBody := map[string]any{"model": p.model, "input": inputs}
	body, _ := json.Marshal(reqBody)
	base, err := url.Parse(p.host)
	if err != nil {
		return nil, err
	}
	// Try /api/embed first
	embedURL := *base
	embedURL.Path = path.Join(embedURL.Path, "/api/embed")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embedURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	// If not 200, try legacy endpoint
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		resp.Body.Close()
		legacyURL := *base
		legacyURL.Path = path.Join(legacyURL.Path, "/api/embeddings")
		req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, legacyURL.String(), bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		resp, err = p.http.Do(req2)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var b struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		if b.Error != "" {
			return nil, fmt.Errorf("ollama error: %s", b.Error)
		}
		return nil, fmt.Errorf("ollama http status: %s", resp.Status)
	}
	// Accept both shapes
	var outEmbed struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&outEmbed); err == nil && len(outEmbed.Embeddings) > 0 {
		return outEmbed.Embeddings, nil
	}
	// Legacy single embedding shape
	var outLegacy struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(bytes.NewReader([]byte{})).Decode(&outLegacy); err != nil {
		// Already consumed body; fallback by re-reading is complex; simplest path: reissue once with single input
	}
	// As a robust fallback: call per-input and batch results
	results := make([][]float32, 0, len(inputs))
	for _, in := range inputs {
		one := map[string]any{"model": p.model, "input": in}
		b2, _ := json.Marshal(one)
		req3, _ := http.NewRequestWithContext(ctx, http.MethodPost, embedURL.String(), bytes.NewReader(b2))
		req3.Header.Set("Content-Type", "application/json")
		r3, err := p.http.Do(req3)
		if err != nil {
			return nil, err
		}
		var single struct {
			Embeddings [][]float32 `json:"embeddings"`
			Embedding  []float64   `json:"embedding"`
		}
		_ = json.NewDecoder(r3.Body).Decode(&single)
		r3.Body.Close()
		if len(single.Embeddings) > 0 {
			results = append(results, single.Embeddings[0])
		} else if len(single.Embedding) > 0 {
			results = append(results, f64to32(single.Embedding))
		} else {
			return nil, fmt.Errorf("ollama returned no embedding")
		}
	}
	return results, nil
}
