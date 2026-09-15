// Package shadow is the M5 silent-quality-failure detector: it scores how
// different a cheap-tier answer is from what the frontier model would have
// said, for a sampled fraction of router-decided cheap-tier requests. See
// docs/ARCHITECTURE.md's "silent-quality check" — a worse-but-well-formed
// cheap answer trips no error or latency alarm; this is the only thing that
// catches it.
//
// This package is pure scoring, synchronous, no network calls (embedding is
// already in-process ONNX) and no HTTP/dispatch knowledge — the orchestration
// (deciding eligibility, calling the frontier model, wiring the result into
// metrics) lives in internal/proxy, which is where the Dispatcher and
// Catalogue already are. See internal/proxy/shadow.go.
package shadow

import (
	"fmt"
	"math/rand"

	"github.com/ngadakh/autoroute/internal/embed"
)

// Embedder is the one method shadow scoring needs — the same shape as
// internal/router.Embedder, so the real ONNX embedder (and the Embedder
// internal/router.BuildClassifier already returns) satisfies this directly,
// no adapter required.
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// Sampler decides which requests to shadow-test and scores the result.
type Sampler struct {
	// Rate is the fraction (0..1) of eligible requests to sample.
	Rate float64
	// Embedder produces the vectors Score compares. Required — a Sampler
	// with a nil Embedder is a configuration bug, not a degrade case (unlike
	// the router's L2 classifier, shadow sampling is only ever wired up when
	// an embedder is already available — see internal/proxy/shadow.go).
	Embedder Embedder
}

// ShouldSample reports whether this request should be shadow-tested — a
// weighted coin flip, not security-sensitive, so package-level math/rand is
// fine.
func (s *Sampler) ShouldSample() bool {
	if s.Rate <= 0 {
		return false
	}
	if s.Rate >= 1 {
		return true
	}
	return rand.Float64() < s.Rate //nolint:gosec // sampling, not a security decision
}

// Score embeds both answers and returns how different they are: 0 means
// identical (as measured by the embedder), larger means more different.
// embed.Cosine expects L2-normalised vectors, which the ONNX embedder
// already produces (see internal/embed.Embedder.Embed).
func (s *Sampler) Score(cheapAnswer, frontierAnswer string) (delta float64, err error) {
	cheapVec, err := s.Embedder.Embed(cheapAnswer)
	if err != nil {
		return 0, fmt.Errorf("embed cheap answer: %w", err)
	}
	frontierVec, err := s.Embedder.Embed(frontierAnswer)
	if err != nil {
		return 0, fmt.Errorf("embed frontier answer: %w", err)
	}
	return 1 - float64(embed.Cosine(cheapVec, frontierVec)), nil
}
