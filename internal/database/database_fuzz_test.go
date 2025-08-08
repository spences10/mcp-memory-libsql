//go:build go1.18

package database

import (
	"testing"
)

// FuzzCoerceVector fuzzes the vector coercion helper for stability.
func FuzzCoerceVector(f *testing.F) {
	f.Add([]byte{1, 2, 3})
	f.Add([]byte{})
	f.Add([]byte{0xff, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		// Try passing random bytes wrapped in interfaces that might occur
		_, _, _ = coerceToFloat32Slice(b)
		_, _, _ = coerceToFloat32Slice([]any{string(b), len(b)})
		_, _, _ = coerceToFloat32Slice([]byte(nil))
		// No panics should occur.
	})
}
