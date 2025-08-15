package database

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/apptype"
)

// vectorZeroString builds a zero vector string for current embedding dims
func (dm *DBManager) vectorZeroString() string {
	if dm.config.EmbeddingDims <= 0 {
		return "[0.0, 0.0, 0.0, 0.0]"
	}
	parts := make([]string, dm.config.EmbeddingDims)
	for i := range parts {
		parts[i] = "0.0"
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
}

// embeddingInputForEntity builds a deterministic text for provider embedding generation
func (dm *DBManager) embeddingInputForEntity(e apptype.Entity) string {
	// Simple heuristic: join observations; providers often expect natural text
	if len(e.Observations) == 0 {
		return e.Name
	}
	return strings.Join(e.Observations, "\n")
}

// vectorToString converts a float32 array to libSQL vector string format
func (dm *DBManager) vectorToString(numbers []float32) (string, error) {
	// If no embedding provided, create a default zero vector
	if len(numbers) == 0 {
		return dm.vectorZeroString(), nil
	}

	// Validate vector dimensions match schema (use configured dims)
	dims := dm.config.EmbeddingDims
	if dims <= 0 {
		dims = 4
	}
	if len(numbers) != dims {
		return "", fmt.Errorf("vector must have exactly %d dimensions, got %d", dims, len(numbers))
	}

	// Validate all elements are finite numbers
	sanitizedNumbers := make([]float32, len(numbers))
	for i, n := range numbers {
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
			log.Printf("Invalid vector value detected, using 0.0 instead of: %f", n)
			sanitizedNumbers[i] = 0.0
		} else {
			sanitizedNumbers[i] = n
		}
	}

	// Convert to string format
	strNumbers := make([]string, len(sanitizedNumbers))
	for i, n := range sanitizedNumbers {
		strNumbers[i] = fmt.Sprintf("%f", n)
	}

	return fmt.Sprintf("[%s]", strings.Join(strNumbers, ", ")), nil
}

// ExtractVector extracts vector from binary format (F32_BLOB)
func (dm *DBManager) ExtractVector(ctx context.Context, embedding []byte) ([]float32, error) {
	if len(embedding) == 0 {
		return nil, nil
	}

	dims := dm.config.EmbeddingDims
	if dims <= 0 {
		dims = 4
	}
	expectedBytes := dims * 4
	if len(embedding) != expectedBytes {
		return nil, fmt.Errorf("invalid embedding size: expected %d bytes for %d-dimensional vector, got %d", expectedBytes, dims, len(embedding))
	}

	vector := make([]float32, dims)
	for i := 0; i < dims; i++ {
		bits := binary.LittleEndian.Uint32(embedding[i*4 : (i+1)*4])
		vector[i] = math.Float32frombits(bits)
	}

	return vector, nil
}

// coerceToFloat32Slice attempts to interpret arbitrary slice-like inputs as a []float32
func coerceToFloat32Slice(value interface{}) ([]float32, bool, error) {
	switch v := value.(type) {
	case []float32:
		out := make([]float32, len(v))
		copy(out, v)
		return out, true, nil
	case []float64:
		out := make([]float32, len(v))
		for i, n := range v {
			out[i] = float32(n)
		}
		return out, true, nil
	case []int:
		out := make([]float32, len(v))
		for i, n := range v {
			out[i] = float32(n)
		}
		return out, true, nil
	case []int64:
		out := make([]float32, len(v))
		for i, n := range v {
			out[i] = float32(n)
		}
		return out, true, nil
	case []interface{}:
		out := make([]float32, len(v))
		for i, elem := range v {
			switch n := elem.(type) {
			case float64:
				out[i] = float32(n)
			case float32:
				out[i] = n
			case int:
				out[i] = float32(n)
			case int64:
				out[i] = float32(n)
			case json.Number:
				f, err := n.Float64()
				if err != nil {
					return nil, false, fmt.Errorf("invalid json.Number at index %d: %v", i, err)
				}
				out[i] = float32(f)
			case string:
				f, err := strconv.ParseFloat(n, 64)
				if err != nil {
					return nil, false, fmt.Errorf("invalid numeric string at index %d: %v", i, err)
				}
				out[i] = float32(f)
			default:
				return nil, false, fmt.Errorf("unsupported vector element type at index %d: %T", i, elem)
			}
		}
		return out, true, nil
	}

	// Try reflection for other slice/array kinds
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		n := rv.Len()
		out := make([]float32, n)
		for i := 0; i < n; i++ {
			el := rv.Index(i).Interface()
			switch x := el.(type) {
			case float64:
				out[i] = float32(x)
			case float32:
				out[i] = x
			case int:
				out[i] = float32(x)
			case int64:
				out[i] = float32(x)
			case json.Number:
				f, err := x.Float64()
				if err != nil {
					return nil, false, fmt.Errorf("invalid json.Number at index %d: %v", i, err)
				}
				out[i] = float32(f)
			case string:
				f, err := strconv.ParseFloat(x, 64)
				if err != nil {
					return nil, false, fmt.Errorf("invalid numeric string at index %d: %v", i, err)
				}
				out[i] = float32(f)
			default:
				return nil, false, fmt.Errorf("unsupported element type at index %d: %T", i, el)
			}
		}
		return out, true, nil
	}

	return nil, false, nil
}


