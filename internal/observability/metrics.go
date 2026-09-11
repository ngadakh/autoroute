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

	RouteDecisions  *prometheus.CounterVec
	DecisionLatency prometheus.Histogram
	RouterDegraded  *prometheus.CounterVec

	buildInfo *prometheus.GaugeVec
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
		RouteDecisions: f.NewCounterVec(prometheus.CounterOpts{
			Name: "autoroute_route_decisions_total",
			Help: "Router decisions, by resolved tier and the layer that decided.",
		}, []string{"tier", "layer"}),
		DecisionLatency: f.NewHistogram(prometheus.HistogramOpts{
			Name: "autoroute_decision_latency_seconds",
			Help: "Wall time the router pipeline adds before dispatch (L1 + optional L2).",
			// L1-only is sub-millisecond; L2 embedding is single-digit ms warm.
			Buckets: []float64{.0001, .0005, .001, .0025, .005, .01, .025, .05, .1, .25},
		}),
		RouterDegraded: f.NewCounterVec(prometheus.CounterOpts{
			Name: "autoroute_router_degraded_total",
			Help: "Requests where the router fell back to the default tier instead of a confident decision.",
		}, []string{"reason"}),
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

// ObserveRouteDecision records one router pipeline decision. degradedReason is
// empty for a confident (non-degraded) decision.
func (m *Metrics) ObserveRouteDecision(tier, layer, degradedReason string, d time.Duration) {
	m.RouteDecisions.WithLabelValues(tier, layer).Inc()
	m.DecisionLatency.Observe(d.Seconds())
	if degradedReason != "" {
		m.RouterDegraded.WithLabelValues(degradedReason).Inc()
	}
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
