package embeddings

import (
	"context"
	"os"
	"strings"
)

// Provider defines a simple embeddings provider interface.
// Implementations should be concurrency-safe.
type Provider interface {
	// Name returns the provider name (e.g., "openai", "ollama").
	Name() string
	// Dimensions returns the embedding dimensionality this provider produces.
	Dimensions() int
	// Embed returns one embedding per input string.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// NewStaticProvider returns a deterministic provider for tests
type StaticProvider struct {
	N int
}

func (s *StaticProvider) Name() string    { return "static" }
func (s *StaticProvider) Dimensions() int { return s.N }
func (s *StaticProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range inputs {
		v := make([]float32, s.N)
		// simple deterministic pattern: decreasing values
		for j := 0; j < s.N; j++ {
			v[j] = float32((s.N - j)) / float32(s.N)
		}
		out[i] = v
	}
	return out, nil
}

// NewFromEnv constructs a provider based on environment variables.
// EMBEDDINGS_PROVIDER: "openai", "ollama", "gemini", "vertexai", "localai", or empty for disabled.
func NewFromEnv() Provider {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDINGS_PROVIDER")))
	switch name {
	case "openai":
		if p := newOpenAIFromEnv(); p != nil {
			return p
		}
		return nil
	case "ollama":
		if p := newOllamaFromEnv(); p != nil {
			return p
		}
		return nil
	case "gemini", "google-gemini", "google_genai", "google":
		if p := newGeminiFromEnv(); p != nil {
			return p
		}
		return nil
	case "vertex", "vertexai", "google-vertex":
		if p := newVertexFromEnv(); p != nil {
			return p
		}
		return nil
	case "localai", "llamacpp", "llama.cpp":
		if p := newLocalAIFromEnv(); p != nil {
			return p
		}
		return nil
	default:
		return nil
	}
}
