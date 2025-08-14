package embeddings

import (
	"context"
	"strings"
)

// adaptingProvider wraps a Provider and coerces its embeddings to a target dimensionality
// by zero-padding or truncating as needed.
type adaptingProvider struct {
	base       Provider
	targetDims int
	mode       string // "pad_or_truncate" (default), "truncate", "pad"
}

// WrapToDims returns a Provider that adapts output vectors to targetDims using the given mode.
// If base already matches targetDims, base is returned unchanged.
func WrapToDims(base Provider, targetDims int, mode string) Provider {
	if base == nil || targetDims <= 0 || base.Dimensions() == targetDims {
		return base
	}
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		m = "pad_or_truncate"
	}
	return &adaptingProvider{base: base, targetDims: targetDims, mode: m}
}

func (p *adaptingProvider) Name() string { return p.base.Name() }

func (p *adaptingProvider) Dimensions() int { return p.targetDims }

func (p *adaptingProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	vecs, err := p.base.Embed(ctx, inputs)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(vecs))
	for i, v := range vecs {
		out[i] = adaptVector(v, p.targetDims, p.mode)
	}
	return out, nil
}

func adaptVector(v []float32, target int, mode string) []float32 {
	if target <= 0 {
		return v
	}
	n := len(v)
	switch mode {
	case "truncate":
		if n <= target {
			// pad to exact size
			out := make([]float32, target)
			copy(out, v)
			return out
		}
		return v[:target]
	case "pad":
		if n >= target {
			return v[:target]
		}
		out := make([]float32, target)
		copy(out, v)
		return out
	default: // pad_or_truncate
		if n == target {
			return v
		}
		if n > target {
			return v[:target]
		}
		out := make([]float32, target)
		copy(out, v)
		return out
	}
}
