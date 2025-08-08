//go:build noprom

package metrics

// When built with -tags noprom, provide a stub that does nothing.
func enablePrometheus(addr string) error { return nil }
