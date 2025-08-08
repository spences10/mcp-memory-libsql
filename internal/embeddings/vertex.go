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

// Vertex AI Text Embeddings API (REST) via AIP
// Endpoint pattern:
// https://{location}-aiplatform.googleapis.com/v1/projects/{project}/locations/{location}/publishers/google/models/{model}:predict
// Requires OAuth2. For simplicity, we allow a direct endpoint+token via env in this implementation.

type vertexProvider struct {
	endpoint string // full URL to :predict
	token    string // OAuth2 access token (bearer)
	dims     int
	http     *http.Client
}

func newVertexFromEnv() Provider {
	endpoint := strings.TrimSpace(os.Getenv("VERTEX_EMBEDDINGS_ENDPOINT"))
	token := strings.TrimSpace(os.Getenv("VERTEX_ACCESS_TOKEN"))
	if endpoint == "" || token == "" {
		return nil
	}
	dims := 768
	return &vertexProvider{endpoint: endpoint, token: token, dims: dims, http: &http.Client{Timeout: 15 * time.Second}}
}

func (p *vertexProvider) Name() string    { return "vertexai" }
func (p *vertexProvider) Dimensions() int { return p.dims }

func (p *vertexProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	// Vertex predict supports batch payloads depending on model; to keep simple, do per-input.
	res := make([][]float32, 0, len(inputs))
	for _, in := range inputs {
		payload := map[string]any{
			"instances": []any{map[string]any{"content": in}},
		}
		b, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.token)
		resp, err := p.http.Do(req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Predictions []struct {
				Embeddings struct {
					Values []float64 `json:"values"`
				} `json:"embeddings"`
			} `json:"predictions"`
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
				return nil, fmt.Errorf("vertex error: %s", er.Error.Message)
			}
			return nil, fmt.Errorf("vertex http status: %s", resp.Status)
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		if len(out.Predictions) == 0 {
			return nil, fmt.Errorf("vertex returned no predictions")
		}
		res = append(res, f64to32(out.Predictions[0].Embeddings.Values))
	}
	return res, nil
}
