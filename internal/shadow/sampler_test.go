package shadow

import (
	"errors"
	"math"
	"testing"

	"github.com/ngadakh/autoroute/internal/embed"
)

// fakeEmbedder maps known text to a fixed vector; anything else errors. Same
// pattern as internal/router/pipeline_test.go's fakeEmbedder.
type fakeEmbedder map[string][]float32

func (f fakeEmbedder) Embed(text string) ([]float32, error) {
	if v, ok := f[text]; ok {
		return v, nil
	}
	return nil, errors.New("fakeEmbedder: no vector for text")
}

func oneHot(i int) []float32 {
	v := make([]float32, embed.Dim)
	v[i] = 1
	return v
}

func approxEqual(a, b, tolerance float64) bool { return math.Abs(a-b) < tolerance }

func TestScoreIdenticalAnswersIsZeroDelta(t *testing.T) {
	e := fakeEmbedder{"same answer": oneHot(0)}
	s := &Sampler{Embedder: e}
	delta, err := s.Score("same answer", "same answer")
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(delta, 0, 1e-9) {
		t.Fatalf("delta = %v, want ~0 for identical answers", delta)
	}
}

func TestScoreOrthogonalAnswersIsMaxDelta(t *testing.T) {
	e := fakeEmbedder{"cheap answer": oneHot(0), "frontier answer": oneHot(1)}
	s := &Sampler{Embedder: e}
	delta, err := s.Score("cheap answer", "frontier answer")
	if err != nil {
		t.Fatal(err)
	}
	if !approxEqual(delta, 1, 1e-9) {
		t.Fatalf("delta = %v, want ~1 for orthogonal (L2-normalised) answers", delta)
	}
}

func TestScoreCheapEmbedErrorPropagates(t *testing.T) {
	e := fakeEmbedder{"frontier answer": oneHot(1)}
	s := &Sampler{Embedder: e}
	if _, err := s.Score("missing", "frontier answer"); err == nil {
		t.Fatal("expected an error when the cheap answer can't be embedded")
	}
}

func TestScoreFrontierEmbedErrorPropagates(t *testing.T) {
	e := fakeEmbedder{"cheap answer": oneHot(0)}
	s := &Sampler{Embedder: e}
	if _, err := s.Score("cheap answer", "missing"); err == nil {
		t.Fatal("expected an error when the frontier answer can't be embedded")
	}
}

func TestShouldSampleBoundaries(t *testing.T) {
	if (&Sampler{Rate: 0}).ShouldSample() {
		t.Fatal("rate 0 should never sample")
	}
	if !(&Sampler{Rate: 1}).ShouldSample() {
		t.Fatal("rate 1 should always sample")
	}
	if (&Sampler{Rate: -1}).ShouldSample() {
		t.Fatal("a negative rate should never sample")
	}
}

func TestShouldSampleStatisticalRate(t *testing.T) {
	s := &Sampler{Rate: 0.3}
	const trials = 20000
	hits := 0
	for i := 0; i < trials; i++ {
		if s.ShouldSample() {
			hits++
		}
	}
	got := float64(hits) / trials
	if got < 0.27 || got > 0.33 {
		t.Fatalf("sampled %v of trials, want ~0.30 (+/- 0.03)", got)
	}
}
