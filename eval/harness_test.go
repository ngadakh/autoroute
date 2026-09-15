package eval

import (
	"math"
	"testing"

	"github.com/ngadakh/autoroute/internal/router"
)

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestHarnessRun hand-verifies the aggregation math on three rows, each
// exercising a different path through the real router.Pipeline (the same
// one internal/proxy/route.go drives in production):
//   - row A: a short factual question -> decided at L1 -> cheap tier.
//   - row B: a formal-proof prompt that L1 never fires on (see
//     internal/router/heuristics_test.go) -> falls to L2, which is nil here
//     (no classifier/embedder wired) -> degrades to the pipeline's
//     DefaultTier (frontier).
//   - row C: a fenced code edit -> decided at L1 -> mid tier, even though
//     the cheap model *could* also have handled it (Score["cheap"]=1) —
//     this is the case CheapModelRecall must NOT count as a hit.
func TestHarnessRun(t *testing.T) {
	cheap := Tiers[router.TierCheap]
	mid := Tiers[router.TierMid]
	frontier := Tiers[router.TierFrontier]

	rows := []Row{
		{
			SampleID: "a", Prompt: "What is the capital of France?",
			Score: map[string]float64{cheap: 1, frontier: 1},
			Cost:  map[string]float64{cheap: 0.0001, frontier: 0.01},
		},
		{
			SampleID: "b", Prompt: "Prove that the sum of the first n odd numbers is n squared.",
			Score: map[string]float64{frontier: 1},
			Cost:  map[string]float64{frontier: 0.01},
		},
		{
			SampleID: "c", Prompt: "Make this idiomatic and add type hints: ```def f(x): return [i for i in x if i%2==0]```",
			Score: map[string]float64{cheap: 1, mid: 0.8, frontier: 1},
			Cost:  map[string]float64{mid: 0.001, frontier: 0.01},
		},
	}

	// No classifier/embedder wired — row B's L2 miss must degrade, not panic.
	pipeline := router.NewPipeline(nil, nil, 0.35, 0.70, router.TierFrontier)
	h := &Harness{Pipeline: pipeline}
	s := h.Run(rows)

	if s.N != 3 {
		t.Fatalf("N = %d, want 3", s.N)
	}
	if !approxEqual(s.RoutedCost, 0.0111) {
		t.Errorf("RoutedCost = %v, want 0.0111", s.RoutedCost)
	}
	if !approxEqual(s.BaselineCost, 0.03) {
		t.Errorf("BaselineCost = %v, want 0.03", s.BaselineCost)
	}
	if !approxEqual(s.RoutedScoreSum, 2.8) {
		t.Errorf("RoutedScoreSum = %v, want 2.8", s.RoutedScoreSum)
	}
	if !approxEqual(s.BaselineScoreSum, 3) {
		t.Errorf("BaselineScoreSum = %v, want 3", s.BaselineScoreSum)
	}
	if !approxEqual(s.Accuracy(), 2.8/3) {
		t.Errorf("Accuracy() = %v, want %v", s.Accuracy(), 2.8/3)
	}
	if !approxEqual(s.BaselineAccuracy(), 1.0) {
		t.Errorf("BaselineAccuracy() = %v, want 1.0", s.BaselineAccuracy())
	}
	if !approxEqual(s.CostSavingsPct(), 63.0) {
		t.Errorf("CostSavingsPct() = %v, want 63.0", s.CostSavingsPct())
	}
	// cheapCapable = rows A and C (Score[cheap]=1); only A was actually
	// routed to cheap (C went to mid) -> recall 1/2.
	if !approxEqual(s.CheapModelRecall, 0.5) {
		t.Errorf("CheapModelRecall = %v, want 0.5", s.CheapModelRecall)
	}
	if s.ByLayer[router.LayerL1] != 2 {
		t.Errorf("ByLayer[L1] = %d, want 2", s.ByLayer[router.LayerL1])
	}
	if s.ByLayer[router.LayerDegradedNoL2] != 1 {
		t.Errorf("ByLayer[degraded-no-l2] = %d, want 1", s.ByLayer[router.LayerDegradedNoL2])
	}
	if s.ByTier[string(router.TierCheap)] != 1 || s.ByTier[string(router.TierMid)] != 1 || s.ByTier[string(router.TierFrontier)] != 1 {
		t.Errorf("ByTier = %+v, want cheap:1 mid:1 frontier:1", s.ByTier)
	}
}

func TestHarnessRunEmpty(t *testing.T) {
	pipeline := router.NewPipeline(nil, nil, 0.35, 0.70, router.TierFrontier)
	h := &Harness{Pipeline: pipeline}
	s := h.Run(nil)
	if s.N != 0 || s.Accuracy() != 0 || s.CostSavingsPct() != 0 {
		t.Fatalf("empty run should be all zeros, got %+v", s)
	}
}
