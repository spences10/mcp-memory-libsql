package metrics

import (
	"os"
	"strconv"
	"sync"
	"sync/atomic"
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
	// Optional: result size metrics (low-cardinality by tool)
	ObserveToolResultSize(tool string, size int)
	// Optional: statement-cache and pool metrics
	IncStmtCacheHit(op string)
	IncStmtCacheMiss(op string)
	ObservePoolStats(inUse, idle int)
}

// noopRecorder implements Recorder with no-ops.
type noopRecorder struct{}

func (n *noopRecorder) IncDBOpTotal(string, bool)                {}
func (n *noopRecorder) ObserveDBOpSeconds(string, bool, float64) {}
func (n *noopRecorder) IncToolTotal(string, bool)                {}
func (n *noopRecorder) ObserveToolSeconds(string, bool, float64) {}
func (n *noopRecorder) ObserveToolResultSize(string, int)        {}
func (n *noopRecorder) IncStmtCacheHit(string)                   {}
func (n *noopRecorder) IncStmtCacheMiss(string)                  {}
func (n *noopRecorder) ObservePoolStats(int, int)                {}

var (
	recMu    sync.RWMutex
	recorder Recorder = &noopRecorder{}
	// serveOnce ensures the metrics HTTP listener and recorder initialization
	// are performed at most once across the process, even if InitFromEnv is
	// called from multiple code paths (e.g., main and server constructors).
	serveOnce sync.Once
	// sampling controls for result-size observations
	sampleEveryN int64    = 1
	toolCounters sync.Map // string -> *uint64
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
// It also starts a small HTTP server on the port configured by METRICS_PORT
// (default 9090) and listens on ":<port>" with endpoints: /metrics (prom)
// and /healthz (200 ok).
func InitFromEnv() {
	// Only proceed when explicitly enabled via env.
	if os.Getenv("METRICS_PROMETHEUS") == "" {
		return
	}
	if v := os.Getenv("METRICS_RESULT_SAMPLE_N"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			sampleEveryN = n
		}
	}
	// Prefer explicit METRICS_PORT (numeric). If unset, default to 9090.
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9090"
	}
	addr := ":" + port
	// Idempotent initialization: ensure listener/recorder starts once.
	serveOnce.Do(func() {
		// Try to install prometheus recorder; if it fails, keep noop.
		_ = enablePrometheus(addr)
	})
}

// ObserveToolResultSize records a histogram of result sizes for a tool, applying
// basic sampling to reduce cardinality/volume. Sampling rate is controlled by
// METRICS_RESULT_SAMPLE_N (default 1 = every call).
func ObserveToolResultSize(tool string, size int) {
	n := sampleEveryN
	if n <= 1 {
		Default().ObserveToolResultSize(tool, size)
		return
	}
	// Per-tool counter
	cPtr, _ := toolCounters.LoadOrStore(tool, new(uint64))
	ctr := cPtr.(*uint64)
	v := atomic.AddUint64(ctr, 1)
	if int64(v)%n == 0 {
		Default().ObserveToolResultSize(tool, size)
	}
}

// enablePrometheus is provided by build-tagged files.
