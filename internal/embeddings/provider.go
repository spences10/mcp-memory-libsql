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
