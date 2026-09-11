//go:build cgo

package router

import (
	"fmt"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/embed"
)

// BuildClassifier constructs the real ONNX-backed L2 embedding classifier and
// the Embedder that feeds it. Requires this binary to be built with
// CGO_ENABLED=1 (`make build-router`) and the ONNX Runtime shared library +
// model fetched (`make setup`).
func BuildClassifier(cfg config.EmbeddingConfig) (*Classifier, Embedder, error) {
	if cfg.ModelPath == "" || cfg.VocabPath == "" {
		return nil, nil, ErrEmbeddingDisabled
	}
	lib := cfg.SharedLibraryPath
	if lib == "" {
		lib = "third_party/onnxruntime/lib/libonnxruntime.dylib"
	}
	if err := embed.InitRuntime(lib); err != nil {
		return nil, nil, fmt.Errorf("init onnx runtime (is %s present? run `make setup`): %w", lib, err)
	}

	maxSeqLen := cfg.MaxSeqLen
	if maxSeqLen <= 0 {
		maxSeqLen = 256
	}
	e, err := embed.NewEmbedder(cfg.ModelPath, cfg.VocabPath, maxSeqLen)
	if err != nil {
		return nil, nil, fmt.Errorf("load embedder: %w", err)
	}
	clf, err := NewClassifier(e, Routes)
	if err != nil {
		return nil, nil, err
	}
	return clf, e, nil
}
