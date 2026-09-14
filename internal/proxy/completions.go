package proxy

import (
	"io"
	"net/http"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/openai"
)

// handleChatCompletions is the core relay. A request naming a catalogue model
// directly is M1-style passthrough: a single candidate, no fallback (though
// its provider's circuit breaker still applies — see internal/reliability).
// A request naming s.TriggerModel (e.g. "auto") is routed to a tier (see
// route.go) and dispatched against the M3 fallback chain: that tier, then
// more capable tiers, then s.PassthroughDefault.
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

	var candidates []config.Model
	resolvedName := req.Model
	if s.Router != nil && req.Model == s.TriggerModel {
		name, tier := s.route(req)
		resolvedName = name
		candidates = s.chainFor(tier)
	} else {
		model, ok := s.Catalogue.Lookup(req.Model)
		if !ok {
			writeError(w, http.StatusNotFound, "unknown model "+strconvQuote(req.Model)+
				"; see GET /v1/models")
			return
		}
		candidates = []config.Model{model}
	}
	if len(candidates) == 0 {
		writeError(w, http.StatusInternalServerError, "router produced no candidate models")
		return
	}

	resp, used, err := s.Dispatcher.Dispatch(r.Context(), candidates, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream "+used.Provider+" unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	s.Logger.Info("forward",
		"model", req.Model, "resolved", resolvedName, "used", used.Name, "upstream", used.Upstream,
		"provider", used.Provider, "stream", req.Stream, "hops", len(candidates))

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
