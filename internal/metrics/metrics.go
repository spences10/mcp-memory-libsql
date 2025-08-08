package metrics

import (
	"os"
	"sync"
	"time"
)

// Package metrics provides a minimal instrumentation interface with a no-op
// default and optional Prometheus-backed implementation enabled via env.

// Recorder defines the metrics surface used across the codebase.
type Recorder interface {
	IncDBOpTotal(op string, success bool)
	ObserveDBOpSeconds(op string, success bool, seconds float64)
	IncToolTotal(tool string, success bool)
	ObserveToolSeconds(tool string, success bool, seconds float64)
}

// noopRecorder implements Recorder with no-ops.
type noopRecorder struct{}

func (n *noopRecorder) IncDBOpTotal(string, bool)                {}
func (n *noopRecorder) ObserveDBOpSeconds(string, bool, float64) {}
func (n *noopRecorder) IncToolTotal(string, bool)                {}
func (n *noopRecorder) ObserveToolSeconds(string, bool, float64) {}

var (
	recMu    sync.RWMutex
	recorder Recorder = &noopRecorder{}
)

// Default returns the current recorder.
func Default() Recorder {
	recMu.RLock()
	defer recMu.RUnlock()
	return recorder
}

// SetRecorder swaps the global recorder implementation.
func SetRecorder(r Recorder) {
	recMu.Lock()
	defer recMu.Unlock()
	recorder = r
}

// TimeOp is a helper to time DB operations.
func TimeOp(op string) func(success bool) {
	start := time.Now()
	return func(success bool) {
		dur := time.Since(start).Seconds()
		Default().IncDBOpTotal(op, success)
		Default().ObserveDBOpSeconds(op, success, dur)
	}
}

// TimeTool is a helper to time tool handler operations.
func TimeTool(tool string) func(success bool) {
	start := time.Now()
	return func(success bool) {
		dur := time.Since(start).Seconds()
		Default().IncToolTotal(tool, success)
		Default().ObserveToolSeconds(tool, success, dur)
	}
}

// InitFromEnv enables Prometheus exporter if METRICS_PROMETHEUS=true.
// It also starts a small HTTP server on METRICS_ADDR (default :9090)
// with endpoints: /metrics (prom) and /healthz (200 ok).
func InitFromEnv() {
	if os.Getenv("METRICS_PROMETHEUS") == "" {
		return
	}
	addr := os.Getenv("METRICS_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	// Try to install prometheus recorder; if it fails, keep noop.
	_ = enablePrometheus(addr)
}

// enablePrometheus is provided by build-tagged files.
