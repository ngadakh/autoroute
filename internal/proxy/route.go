package proxy

import (
	"time"

	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/openai"
	"github.com/ngadakh/autoroute/internal/router"
)

// route resolves a "model": s.TriggerModel request to a concrete catalogue
// model name via the layered router (L1 heuristics, then L2 embedding +
// confidence bands if available). It never fails the request: an unmapped or
// unrecognised tier falls back to s.Router.DefaultTier, and that in turn is
// guaranteed present in s.TierModels by catalogue validation
// (RouterConfig.validate). Records the decision to metrics and the decision
// log.
func (s *Server) route(req openai.Request) string {
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

	modelName, ok := s.TierModels[result.Tier]
	if !ok {
		// Should not happen — RouterConfig.validate requires DefaultTier to be
		// a key in Tiers — but never fail the request over a router bug.
		modelName = s.TierModels[s.Router.DefaultTier]
	}
	return modelName
}
