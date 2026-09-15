package eval

import (
	"github.com/ngadakh/autoroute/internal/openai"
	"github.com/ngadakh/autoroute/internal/router"
)

// Tiers maps AutoRoute's three tiers onto RouterBench model names, picked by
// cost from the dataset itself: mistral-7b-chat is the cheapest model
// RouterBench scored, gpt-3.5-turbo-1106 a mid-cost/mid-quality model, and
// gpt-4-1106-preview the highest-quality (and most expensive) — a ~71x cost
// spread between cheap and frontier. Exported so cmd/eval and tests share
// one source of truth.
var Tiers = map[router.Tier]string{
	router.TierCheap:    "mistralai/mistral-7b-chat",
	router.TierMid:      "gpt-3.5-turbo-1106",
	router.TierFrontier: "gpt-4-1106-preview",
}

// Harness replays rows through a router.Pipeline the same way
// internal/proxy/route.go does in production — ExtractSignals -> RouteL1 ->
// (on a miss) RouteL2 — so eval fidelity is real, not a reimplementation
// that could quietly drift from what actually ships.
type Harness struct {
	Pipeline *router.Pipeline
}

// Run replays every row and aggregates the router's cost/accuracy against an
// always-frontier baseline (the same prompts, always billed and scored as
// the frontier model would have been).
func (h *Harness) Run(rows []Row) Summary {
	s := Summary{ByLayer: make(map[string]int), ByTier: make(map[string]int), N: len(rows)}

	frontierModel := Tiers[router.TierFrontier]
	cheapModel := Tiers[router.TierCheap]

	var cheapCapable, cheapCapableAndRouted int

	for _, row := range rows {
		req := openai.Request{
			Model:    "auto",
			Messages: []openai.Message{{Role: "user", Content: row.Prompt}},
		}
		sig := router.ExtractSignals(req)
		result, ok := h.Pipeline.RouteL1(sig)
		if !ok {
			result = h.Pipeline.RouteL2(sig.Text)
		}
		s.ByLayer[result.Layer]++
		s.ByTier[string(result.Tier)]++

		modelName, ok := Tiers[result.Tier]
		if !ok {
			// Shouldn't happen — Tiers covers every router.Tier constant —
			// but never let an unmapped tier silently drop a row's cost.
			modelName = frontierModel
		}

		s.RoutedCost += row.Cost[modelName]
		s.RoutedScoreSum += row.Score[modelName]
		s.BaselineCost += row.Cost[frontierModel]
		s.BaselineScoreSum += row.Score[frontierModel]

		// >= 0.5 treats a benchmark's own correctness threshold as "could
		// have handled it" — most RouterBench scores are exactly 0/1, but
		// the MT-Bench variants are continuous in [0,1].
		if row.Score[cheapModel] >= 0.5 {
			cheapCapable++
			if modelName == cheapModel {
				cheapCapableAndRouted++
			}
		}
	}

	if cheapCapable > 0 {
		s.CheapModelRecall = float64(cheapCapableAndRouted) / float64(cheapCapable)
	}
	return s
}
