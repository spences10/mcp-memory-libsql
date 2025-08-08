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

// Gemini Generative Language API embeddings
// Docs: https://ai.google.dev/api/embeddings
// Endpoint: https://generativelanguage.googleapis.com/v1beta/models/{model}:embedContent?key=API_KEY

type geminiProvider struct {
	apiKey string
	model  string
	dims   int
	http   *http.Client
}

func newGeminiFromEnv() Provider {
	apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	if apiKey == "" {
		return nil
	}
	model := os.Getenv("GEMINI_EMBEDDINGS_MODEL")
	if model == "" {
		// Common: text-embedding-004
		model = "text-embedding-004"
	}
	dims := 768
	if strings.Contains(model, "004") {
		dims = 768
	}
	return &geminiProvider{apiKey: apiKey, model: model, dims: dims, http: &http.Client{Timeout: 15 * time.Second}}
}

func (p *geminiProvider) Name() string    { return "gemini" }
func (p *geminiProvider) Dimensions() int { return p.dims }

func (p *geminiProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	// Batch not officially supported in one call for embedContent; loop
	res := make([][]float32, 0, len(inputs))
	for _, in := range inputs {
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent?key=%s", p.model, p.apiKey)
		// Payload per docs: {"content": {"parts": [{"text": "..."}]}}
		payload := map[string]any{
			"content": map[string]any{"parts": []map[string]string{{"text": in}}},
		}
		b, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.http.Do(req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Embedding struct {
				Values []float64 `json:"values"`
			} `json:"embedding"`
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var er struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&er)
			resp.Body.Close()
			if er.Error.Message != "" {
				return nil, fmt.Errorf("gemini error: %s", er.Error.Message)
			}
			return nil, fmt.Errorf("gemini http status: %s", resp.Status)
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		res = append(res, f64to32(out.Embedding.Values))
	}
	return res, nil
}
