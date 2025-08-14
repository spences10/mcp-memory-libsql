package embeddings

import (
	"context"
	"os"
	"strconv"
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
	targetDims := 0
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_DIMS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			targetDims = n
		}
	}
	// optional policy for size adaptation
	adaptMode := strings.TrimSpace(os.Getenv("EMBEDDINGS_ADAPT_MODE")) // "pad_or_truncate" | "truncate" | "pad"
	switch name {
	case "openai":
		if p := newOpenAIFromEnv(); p != nil {
			return maybeWrap(p, targetDims, adaptMode)
		}
		return nil
	case "ollama":
		if p := newOllamaFromEnv(); p != nil {
			return maybeWrap(p, targetDims, adaptMode)
		}
		return nil
	case "gemini", "google-gemini", "google_genai", "google":
		if p := newGeminiFromEnv(); p != nil {
			return maybeWrap(p, targetDims, adaptMode)
		}
		return nil
	case "vertex", "vertexai", "google-vertex":
		if p := newVertexFromEnv(); p != nil {
			return maybeWrap(p, targetDims, adaptMode)
		}
		return nil
	case "localai", "llamacpp", "llama.cpp":
		if p := newLocalAIFromEnv(); p != nil {
			return maybeWrap(p, targetDims, adaptMode)
		}
		return nil
	case "voyage", "voyageai", "voyage-ai":
		if p := newVoyageFromEnv(); p != nil {
			return maybeWrap(p, targetDims, adaptMode)
		}
		return nil
	default:
		return nil
	}
}

func maybeWrap(p Provider, targetDims int, mode string) Provider {
	if targetDims <= 0 || p == nil || p.Dimensions() == targetDims {
		return p
	}
	return WrapToDims(p, targetDims, mode)
}
