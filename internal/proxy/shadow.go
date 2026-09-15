package proxy

import (
	"context"
	"io"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/openai"
	"github.com/ngadakh/autoroute/internal/router"
)

// maybeShadow decides whether this response should be shadow-tested and, if
// so, launches the comparison in its own goroutine — it must never block or
// affect the client-facing response, which has already been served by the
// time this is called. tier is the tier the router actually decided for
// this request (the zero value for a direct-named, unrouted request, which
// is therefore never shadow-eligible — only router-decided cheap-tier
// traffic is, per docs/ARCHITECTURE.md). rawBody is the original client
// request body, unrewritten — the shadow call re-dispatches exactly that to
// the frontier model.
func (s *Server) maybeShadow(tier router.Tier, rawBody []byte, cheapAnswer string) {
	if s.Shadow == nil || tier != router.TierCheap || !s.Shadow.ShouldSample() {
		return
	}
	go s.runShadow(rawBody, cheapAnswer)
}

// runShadow performs one shadow comparison: calls the frontier model,
// scores the delta against the cheap answer, and records/logs it. Every
// failure path logs a warning and returns — a shadow-sample failure must
// never surface anywhere the (long-gone) client or the main request path
// can see.
func (s *Server) runShadow(rawBody []byte, cheapAnswer string) {
	ctx := context.Background() // outlives the client's request/response cycle

	frontierName, ok := s.TierModels[router.TierFrontier]
	if !ok {
		s.Logger.Warn("shadow sample: no frontier tier model configured")
		return
	}
	frontierModel, ok := s.Catalogue.Lookup(frontierName)
	if !ok {
		s.Logger.Warn("shadow sample: frontier model not in catalogue", "model", frontierName)
		return
	}

	resp, _, err := s.Dispatcher.Dispatch(ctx, []config.Model{frontierModel}, rawBody)
	if err != nil {
		s.Logger.Warn("shadow sample: frontier call failed", "err", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.Logger.Warn("shadow sample: read frontier response failed", "err", err)
		return
	}
	frontierAnswer, ok := openai.Answer(body)
	if !ok {
		s.Logger.Warn("shadow sample: could not extract frontier answer text")
		return
	}

	delta, err := s.Shadow.Score(cheapAnswer, frontierAnswer)
	if err != nil {
		s.Logger.Warn("shadow sample: scoring failed", "err", err)
		return
	}
	s.Metrics.ObserveShadowDelta(delta)
	if delta > s.ShadowAlertThreshold {
		s.Logger.Warn("shadow sample: quality delta exceeds threshold",
			"delta", delta, "threshold", s.ShadowAlertThreshold)
	}
}
