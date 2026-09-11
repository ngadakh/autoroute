package router

import (
	"errors"
	"testing"

	"github.com/ngadakh/autoroute/internal/embed"
)

// fakeEmbedder maps known text to a fixed vector; anything else errors. It
// lets pipeline/classifier tests run with no ONNX Runtime / cgo — the same
// pattern cmd/spike-embed's real Embedder satisfies via the Embedder
// interface in classify.go.
type fakeEmbedder struct {
	vectors map[string][]float32
}

func (f fakeEmbedder) Embed(text string) ([]float32, error) {
	v, ok := f.vectors[text]
	if !ok {
		return nil, errors.New("fakeEmbedder: no vector for text")
	}
	return v, nil
}

// twoRoutes is a minimal two-route taxonomy so exemplar/query vectors are easy
// to reason about: each exemplar and the two probe vectors are one-hot-ish in
// a 2D-in-embed.Dim space.
var twoRoutes = []Route{
	{Name: "route-a", Tier: TierCheap, Exemplars: []string{"exemplar-a"}},
	{Name: "route-b", Tier: TierFrontier, Exemplars: []string{"exemplar-b"}},
}

func vec(first, second float32) []float32 {
	v := make([]float32, embed.Dim)
	v[0], v[1] = first, second
	return v
}

func newTestClassifier(t *testing.T) *Classifier {
	t.Helper()
	e := fakeEmbedder{vectors: map[string][]float32{
		"exemplar-a": vec(1, 0),
		"exemplar-b": vec(0, 1),
	}}
	clf, err := NewClassifier(e, twoRoutes)
	if err != nil {
		t.Fatal(err)
	}
	return clf
}

func TestPipelineRouteL2ConfidenceBands(t *testing.T) {
	clf := newTestClassifier(t)

	cases := []struct {
		name      string
		query     []float32
		wantTier  Tier
		wantLayer string
	}{
		{
			name:      "clear match to route-a",
			query:     vec(1, 0),
			wantTier:  TierCheap,
			wantLayer: LayerL2,
		},
		{
			name:      "clear match to route-b",
			query:     vec(0, 1),
			wantTier:  TierFrontier,
			wantLayer: LayerL2,
		},
		{
			// centroids are exactly (1,0) and (0,1), so with an unnormalised
			// query Cosine(query, centroid) is just the matching component:
			// top-2 margin = 0.55-0.50 = 0.05 -> confidence 0.05/0.15 = 0.33,
			// inside [theta_low, theta_high) = [0.2, 0.6).
			name:      "margin inside the uncertain band -> conservative default",
			query:     vec(0.55, 0.50),
			wantTier:  TierFrontier, // pipeline's configured default
			wantLayer: LayerL2BandDefault,
		},
	}

	e := fakeEmbedder{vectors: map[string][]float32{"probe": nil}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e.vectors["probe"] = tc.query
			p := NewPipeline(clf, e, 0.2, 0.6, TierFrontier)
			res := p.RouteL2("probe")
			if res.Tier != tc.wantTier || res.Layer != tc.wantLayer {
				t.Fatalf("got tier=%q layer=%q conf=%.3f, want tier=%q layer=%q",
					res.Tier, res.Layer, res.Confidence, tc.wantTier, tc.wantLayer)
			}
		})
	}
}

func TestPipelineRouteL2LowConfidenceDegradesToDefault(t *testing.T) {
	clf := newTestClassifier(t)
	// theta_low high enough that even the ambiguous probe lands below it.
	e := fakeEmbedder{vectors: map[string][]float32{"probe": vec(0.71, 0.70)}}
	p := NewPipeline(clf, e, 0.95, 0.99, TierCheap)

	res := p.RouteL2("probe")
	if res.Tier != TierCheap || res.Layer != LayerDegradedLowConf {
		t.Fatalf("got tier=%q layer=%q, want tier=%q layer=%q", res.Tier, res.Layer, TierCheap, LayerDegradedLowConf)
	}
}

func TestPipelineRouteL2NoClassifierDegrades(t *testing.T) {
	p := NewPipeline(nil, nil, 0.2, 0.6, TierMid)
	res := p.RouteL2("anything")
	if res.Tier != TierMid || res.Layer != LayerDegradedNoL2 {
		t.Fatalf("got tier=%q layer=%q, want tier=%q layer=%q", res.Tier, res.Layer, TierMid, LayerDegradedNoL2)
	}
}

func TestPipelineRouteL2EmbedErrorDegrades(t *testing.T) {
	clf := newTestClassifier(t)
	p := NewPipeline(clf, fakeEmbedder{vectors: map[string][]float32{}}, 0.2, 0.6, TierMid)
	res := p.RouteL2("no such key")
	if res.Tier != TierMid || res.Layer != LayerDegradedNoL2 {
		t.Fatalf("got tier=%q layer=%q, want tier=%q layer=%q", res.Tier, res.Layer, TierMid, LayerDegradedNoL2)
	}
}

func TestPipelineRouteL1PreemptsL2(t *testing.T) {
	p := NewPipeline(nil, nil, 0.2, 0.6, TierFrontier)
	res, ok := p.RouteL1(L1Signals{Words: 3, Text: "who is that"})
	if !ok || res.Layer != LayerL1 {
		t.Fatalf("got ok=%v layer=%q, want a confident L1 hit", ok, res.Layer)
	}
}
