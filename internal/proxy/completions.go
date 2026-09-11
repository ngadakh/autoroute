package proxy

import (
	"io"
	"net/http"
	"time"

	"github.com/ngadakh/autoroute/internal/openai"
)

// handleChatCompletions is the core relay. A request naming a catalogue model
// directly is M1-style passthrough — the proxy rewrites it to the upstream id
// and streams the provider's response back. A request naming
// s.TriggerModel (e.g. "auto") is routed to a tier first (see route.go).
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large or unreadable")
		return
	}

	req, err := openai.Peek(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resolvedName := req.Model
	if s.Router != nil && req.Model == s.TriggerModel {
		resolvedName = s.route(req)
	}

	model, ok := s.Catalogue.Lookup(resolvedName)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown model "+strconvQuote(resolvedName)+
			"; see GET /v1/models")
		return
	}
	prov, ok := s.Providers.Get(model.Provider)
	if !ok {
		writeError(w, http.StatusInternalServerError, "provider "+model.Provider+" not configured")
		return
	}

	upstreamBody, err := openai.WithModel(body, model.Upstream)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not rewrite model field: "+err.Error())
		return
	}

	s.Logger.Info("forward",
		"model", req.Model, "resolved", resolvedName, "upstream", model.Upstream,
		"provider", model.Provider, "stream", req.Stream)

	start := time.Now()
	resp, err := prov.Do(r.Context(), upstreamBody)
	if err != nil {
		s.Metrics.ObserveUpstream(model.Provider, model.Upstream, 0, time.Since(start), err)
		writeError(w, http.StatusBadGateway, "upstream "+model.Provider+" unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	s.Metrics.ObserveUpstream(model.Provider, model.Upstream, resp.StatusCode, time.Since(start), nil)

	relay(w, resp, req.Stream)
}

// relay copies the upstream response to the client. For streams it forwards
// bytes as they arrive and flushes; for unary responses it copies the body.
func relay(w http.ResponseWriter, resp *http.Response, stream bool) {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if stream {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	}
	w.WriteHeader(resp.StatusCode)

	rc := http.NewResponseController(w)
	buf := make([]byte, 16<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client went away
			}
			if stream {
				_ = rc.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

func strconvQuote(s string) string { return `"` + s + `"` }
