// Package observability wires up the metrics AutoRoute exposes on /metrics. M1
// covers the proxy edge (HTTP in, upstream out); routing, fallback and shadow
// metrics arrive with their milestones.
package observability

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics is the set of collectors, bound to one registry so tests can isolate.
type Metrics struct {
	Registry *prometheus.Registry

	HTTPRequests   *prometheus.CounterVec
	HTTPDuration   *prometheus.HistogramVec
	UpstreamReqs   *prometheus.CounterVec
	UpstreamErrors *prometheus.CounterVec
	UpstreamDur    *prometheus.HistogramVec
	buildInfo      *prometheus.GaugeVec
}

// New builds a Metrics bound to a fresh registry (plus Go/process collectors).
func New(version string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	f := promauto.With(reg)

	m := &Metrics{
		Registry: reg,
		HTTPRequests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "autoroute_http_requests_total",
			Help: "HTTP requests handled by the proxy edge.",
		}, []string{"route", "method", "code"}),
		HTTPDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "autoroute_http_request_duration_seconds",
			Help:    "Wall time to serve an HTTP request, proxy edge.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		UpstreamReqs: f.NewCounterVec(prometheus.CounterOpts{
			Name: "autoroute_upstream_requests_total",
			Help: "Chat-completion requests sent to an upstream provider.",
		}, []string{"provider", "model", "code"}),
		UpstreamErrors: f.NewCounterVec(prometheus.CounterOpts{
			Name: "autoroute_upstream_errors_total",
			Help: "Upstream calls that failed before a response (transport, timeout, cancel).",
		}, []string{"provider", "model"}),
		UpstreamDur: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "autoroute_upstream_request_duration_seconds",
			Help:    "Time to first response from an upstream provider.",
			Buckets: prometheus.DefBuckets,
		}, []string{"provider", "model"}),
		buildInfo: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "autoroute_build_info",
			Help: "Build metadata; value is always 1.",
		}, []string{"version"}),
	}
	m.buildInfo.WithLabelValues(version).Set(1)
	return m
}

// ObserveHTTP records one served HTTP request.
func (m *Metrics) ObserveHTTP(route, method string, code int, d time.Duration) {
	m.HTTPRequests.WithLabelValues(route, method, strconv.Itoa(code)).Inc()
	m.HTTPDuration.WithLabelValues(route).Observe(d.Seconds())
}

// ObserveUpstream records one upstream call. code == 0 means no response.
func (m *Metrics) ObserveUpstream(provider, model string, code int, d time.Duration, err error) {
	if err != nil {
		m.UpstreamErrors.WithLabelValues(provider, model).Inc()
		return
	}
	m.UpstreamReqs.WithLabelValues(provider, model, strconv.Itoa(code)).Inc()
	m.UpstreamDur.WithLabelValues(provider, model).Observe(d.Seconds())
}
