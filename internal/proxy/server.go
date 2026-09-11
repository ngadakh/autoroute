// Package proxy is the OpenAI-compatible HTTP edge: it accepts
// /v1/chat/completions, forwards to the provider named by the catalogue, and
// relays the response (including SSE streams). Since M2, a request naming the
// catalogue's router.trigger_model (e.g. "auto") is routed to a tier by the
// layered router before forwarding; any other model name is M1-style direct
// passthrough.
package proxy

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/provider"
	"github.com/ngadakh/autoroute/internal/router"
)

// Server holds everything the HTTP handlers need.
type Server struct {
	Catalogue *config.Catalogue
	Providers provider.Set
	Metrics   *observability.Metrics
	Logger    *slog.Logger
	Version   string

	// Router is nil when the catalogue has no router: block, or router.enabled
	// is false — every request is then M1-style direct passthrough.
	Router       *router.Pipeline
	TierModels   map[router.Tier]string     // tier -> catalogue model name
	TriggerModel string                     // client-facing "model" that opts into routing
	DecisionLog  *observability.DecisionLog // nil disables decision logging

	// maxBodyBytes caps an incoming request body (long-context prompts are big).
	maxBodyBytes int64
	ready        atomic.Bool
}

// New assembles a Server. Call SetReady(true) once startup work is done.
func New(cat *config.Catalogue, providers provider.Set, m *observability.Metrics, logger *slog.Logger, version string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		Catalogue:    cat,
		Providers:    providers,
		Metrics:      m,
		Logger:       logger,
		Version:      version,
		maxBodyBytes: 10 << 20, // 10 MiB
	}
}

// SetReady flips the /readyz signal. Set false during graceful shutdown so load
// balancers drain this instance before the process exits.
func (s *Server) SetReady(v bool) { s.ready.Store(v) }

// Handler returns the routed, instrumented http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.Metrics.Registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /{$}", s.handleRoot)
	return s.instrument(mux)
}

// instrument records per-request metrics and an access log line, and recovers
// panics into a 500 so one bad handler can't take the process down.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if p := recover(); p != nil {
				s.Logger.Error("panic in handler", "panic", p, "path", r.URL.Path)
				if !rec.wrote {
					writeError(rec, http.StatusInternalServerError, "internal error")
				}
			}
			route := routeLabel(r)
			d := time.Since(start)
			s.Metrics.ObserveHTTP(route, r.Method, rec.status, d)
			if route != "/metrics" && route != "/healthz" && route != "/readyz" {
				s.Logger.Info("request",
					"method", r.Method, "route", route,
					"status", rec.status, "dur_ms", d.Milliseconds())
			}
		}()

		next.ServeHTTP(rec, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "autoroute", "version": s.Version,
		"models": s.Catalogue.ModelNames(),
		"docs":   "https://github.com/ngadakh/autoroute",
	})
}

// handleModels returns an OpenAI-shaped model list from the catalogue.
func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]model, 0, len(s.Catalogue.Models))
	for _, m := range s.Catalogue.Models {
		data = append(data, model{ID: m.Name, Object: "model", OwnedBy: "autoroute"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// --- helpers ---

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer for Flush.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func routeLabel(r *http.Request) string {
	if p := r.Pattern; p != "" {
		// r.Pattern is like "POST /v1/chat/completions"; keep the path part.
		for i := 0; i < len(p); i++ {
			if p[i] == '/' {
				return p[i:]
			}
		}
	}
	return r.URL.Path
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits an OpenAI-shaped error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": errType(status)},
	})
}

func errType(status int) string {
	switch {
	case status == http.StatusNotFound:
		return "invalid_request_error"
	case status == http.StatusBadRequest:
		return "invalid_request_error"
	case status >= 500:
		return "api_error"
	default:
		return "error"
	}
}
