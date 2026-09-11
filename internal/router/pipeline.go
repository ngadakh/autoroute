package router

// Layer names recorded in a Result and the decision log — mirrors the
// decision-gate diagram (g1/g2) in docs/ARCHITECTURE.md.
const (
	LayerL1              = "L1"
	LayerL2              = "L2"
	LayerL2BandDefault   = "L2-band-default"
	LayerDegradedNoL2    = "degraded-no-l2"
	LayerDegradedLowConf = "degraded-low-confidence"
)

// Result is the router's decision for one request.
type Result struct {
	Tier       Tier
	Layer      string
	Confidence float64
	Route      string // L2 route name; empty for L1 and degraded results
	Reason     string
}

// Pipeline orchestrates L1 heuristics -> L2 embedding classifier -> confidence
// bands. Classifier/Embedder may be nil — L2 unavailable, either a
// CGO_ENABLED=0 build (see embedder_stub.go) or the embedder failed to load —
// in which case every L1 miss resolves to DefaultTier: the
// degrade-to-passthrough path, not an error.
type Pipeline struct {
	Classifier  *Classifier
	Embedder    Embedder
	ThetaLow    float64
	ThetaHigh   float64
	DefaultTier Tier
}

// NewPipeline builds a Pipeline. classifier and embedder may be nil together
// (both come from the same BuildClassifier call).
func NewPipeline(classifier *Classifier, embedder Embedder, thetaLow, thetaHigh float64, defaultTier Tier) *Pipeline {
	return &Pipeline{Classifier: classifier, Embedder: embedder, ThetaLow: thetaLow, ThetaHigh: thetaHigh, DefaultTier: defaultTier}
}

// RouteL1 tries the heuristic layer only. Call this first: computing a prompt
// embedding for RouteL2 costs orders of magnitude more than L1, so a request
// that resolves here never touches the embedder — "zero embedding calls on
// the common path".
func (p *Pipeline) RouteL1(sig L1Signals) (Result, bool) {
	tier, reason, ok := L1Classify(sig)
	if !ok {
		return Result{}, false
	}
	return Result{Tier: tier, Layer: LayerL1, Confidence: 1, Reason: reason}, true
}

// RouteL2 embeds text and classifies it against the confidence bands. Only
// call this after RouteL1 has missed.
func (p *Pipeline) RouteL2(text string) Result {
	if p.Classifier == nil || p.Embedder == nil {
		return Result{Tier: p.DefaultTier, Layer: LayerDegradedNoL2, Reason: "embedding classifier unavailable"}
	}
	vec, err := p.Embedder.Embed(text)
	if err != nil {
		return Result{Tier: p.DefaultTier, Layer: LayerDegradedNoL2, Reason: "embed error: " + err.Error()}
	}

	d := p.Classifier.Classify(vec)
	switch {
	case d.Confidence >= p.ThetaHigh:
		return Result{Tier: d.Tier, Layer: LayerL2, Confidence: d.Confidence, Route: d.Route, Reason: "confident L2 match"}
	case d.Confidence >= p.ThetaLow:
		return Result{Tier: p.DefaultTier, Layer: LayerL2BandDefault, Confidence: d.Confidence, Route: d.Route, Reason: "uncertain band, conservative default"}
	default:
		// Below theta_low the design hands off to an L3 judge (see
		// docs/ARCHITECTURE.md) — that layer doesn't exist yet, so this also
		// resolves to the conservative default rather than a silent guess.
		// Forward-compatible slot, not a stub standing in for it.
		return Result{Tier: p.DefaultTier, Layer: LayerDegradedLowConf, Confidence: d.Confidence, Route: d.Route, Reason: "low confidence, no L3 judge yet, conservative default"}
	}
}
