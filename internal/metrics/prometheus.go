//go:build !noprom

package metrics

import (
	"fmt"
	"net/http"

	prom "github.com/prometheus/client_golang/prometheus"
	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"
)

type promRecorder struct {
	dbTotal     *prom.CounterVec
	dbSeconds   *prom.HistogramVec
	toolTotal   *prom.CounterVec
	toolSeconds *prom.HistogramVec
}

func (p *promRecorder) IncDBOpTotal(op string, success bool) {
	p.dbTotal.WithLabelValues(op, fmt.Sprintf("%t", success)).Inc()
}

func (p *promRecorder) ObserveDBOpSeconds(op string, success bool, seconds float64) {
	p.dbSeconds.WithLabelValues(op, fmt.Sprintf("%t", success)).Observe(seconds)
}

func (p *promRecorder) IncToolTotal(tool string, success bool) {
	p.toolTotal.WithLabelValues(tool, fmt.Sprintf("%t", success)).Inc()
}

func (p *promRecorder) ObserveToolSeconds(tool string, success bool, seconds float64) {
	p.toolSeconds.WithLabelValues(tool, fmt.Sprintf("%t", success)).Observe(seconds)
}

func enablePrometheus(addr string) error {
	registry := prom.NewRegistry()
	p := &promRecorder{
		dbTotal: prom.NewCounterVec(prom.CounterOpts{
			Name: "db_ops_total",
			Help: "Total number of DB operations",
		}, []string{"op", "success"}),
		dbSeconds: prom.NewHistogramVec(prom.HistogramOpts{
			Name:    "db_op_seconds",
			Help:    "DB operation duration in seconds",
			Buckets: prom.DefBuckets,
		}, []string{"op", "success"}),
		toolTotal: prom.NewCounterVec(prom.CounterOpts{
			Name: "tool_calls_total",
			Help: "Total number of tool handler calls",
		}, []string{"tool", "success"}),
		toolSeconds: prom.NewHistogramVec(prom.HistogramOpts{
			Name:    "tool_call_seconds",
			Help:    "Tool handler duration in seconds",
			Buckets: prom.DefBuckets,
		}, []string{"tool", "success"}),
	}

	registry.MustRegister(p.dbTotal, p.dbSeconds, p.toolTotal, p.toolSeconds)
	SetRecorder(p)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	go func() { _ = http.ListenAndServe(addr, mux) }()
	return nil
}
