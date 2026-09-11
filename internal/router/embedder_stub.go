//go:build !cgo

package router

import "github.com/ngadakh/autoroute/internal/config"

// BuildClassifier stub for CGO_ENABLED=0 builds (the default `make build` /
// Docker image): the real ONNX embedder needs cgo (see embedder_cgo.go), so
// L2 is always unavailable here. The pipeline still runs — L1 heuristics plus
// every L1 miss resolving to the configured default tier, i.e.
// degrade-to-passthrough, not a build/startup error.
func BuildClassifier(cfg config.EmbeddingConfig) (*Classifier, Embedder, error) {
	return nil, nil, ErrEmbeddingDisabled
}
