package proxy

import (
	"time"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/openai"
	"github.com/ngadakh/autoroute/internal/router"
)

// route resolves a "model": s.TriggerModel request to a concrete catalogue
// model name and the tier that produced it, via the layered router (L1
// heuristics, then L2 embedding + confidence bands if available). It never
// fails the request: an unmapped or unrecognised tier falls back to
// s.Router.DefaultTier, and that in turn is guaranteed present in
// s.TierModels by catalogue validation (RouterConfig.validate). Records the
// decision to metrics and the decision log. The returned tier feeds
// chainFor, which builds the M3 fallback chain starting from it.
func (s *Server) route(req openai.Request) (modelName string, tier router.Tier) {
	start := time.Now()

	sig := router.ExtractSignals(req)
	result, ok := s.Router.RouteL1(sig)
	if !ok {
		result = s.Router.RouteL2(sig.Text)
	}

	elapsed := time.Since(start)
	degradedReason := ""
	if result.Layer == router.LayerDegradedNoL2 || result.Layer == router.LayerDegradedLowConf {
		degradedReason = result.Layer
	}
	s.Metrics.ObserveRouteDecision(string(result.Tier), result.Layer, degradedReason, elapsed)

	if s.DecisionLog != nil {
		if err := s.DecisionLog.Append(observability.DecisionEntry{
			Time:       start,
			Tier:       string(result.Tier),
			Layer:      result.Layer,
			Confidence: result.Confidence,
			Route:      result.Route,
			Reason:     result.Reason,
			Degraded:   degradedReason != "",
			LatencyMS:  float64(elapsed) / float64(time.Millisecond),
		}); err != nil {
			s.Logger.Warn("decision log write failed", "err", err)
		}
	}

	tier = result.Tier
	name, ok := s.TierModels[tier]
	if !ok {
		// Should not happen — RouterConfig.validate requires DefaultTier to be
		// a key in Tiers — but never fail the request over a router bug.
		tier = s.Router.DefaultTier
		name = s.TierModels[tier]
	}
	return name, tier
}

// fallbackOrder is the fixed climb the M3 fallback chain uses: on failure,
// always move toward a more capable tier, never down to a cheaper one. See
// the "cheap -> mid -> frontier -> passthrough default" chain in
// docs/ARCHITECTURE.md.
var fallbackOrder = []router.Tier{router.TierCheap, router.TierMid, router.TierFrontier}

// chainFor builds the ordered dispatch candidates for a routed request,
// starting at tier and climbing fallbackOrder, with s.PassthroughDefault
// appended as the final safety net. Consecutive candidates that resolve to
// the same (Provider, Upstream) — e.g. two tiers sharing a model in a demo
// catalogue — are deduplicated.
func (s *Server) chainFor(tier router.Tier) []config.Model {
	start := 0
	for i, t := range fallbackOrder {
		if t == tier {
			start = i
			break
		}
	}

	names := make([]string, 0, len(fallbackOrder)-start+1)
	for _, t := range fallbackOrder[start:] {
		if name, ok := s.TierModels[t]; ok {
			names = append(names, name)
		}
	}
	names = append(names, s.PassthroughDefault)

	chain := make([]config.Model, 0, len(names))
	for _, name := range names {
		m, ok := s.Catalogue.Lookup(name)
		if !ok {
			continue // shouldn't happen — validated at load time
		}
		if n := len(chain); n > 0 && chain[n-1].Provider == m.Provider && chain[n-1].Upstream == m.Upstream {
			continue // dedupe a consecutive identical hop
		}
		chain = append(chain, m)
	}
	return chain
}
