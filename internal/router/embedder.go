package router

import "errors"

// ErrEmbeddingDisabled is returned by BuildClassifier when the L2 embedding
// classifier isn't available: either this binary was built with
// CGO_ENABLED=0 (see embedder_stub.go — the real ONNX embedder needs cgo,
// see embedder_cgo.go), or embedding.model_path/vocab_path is unset in the
// catalogue's router config. Callers treat it as "run L1-only", not a fatal
// startup error — the router degrades to the conservative default tier past
// L1 instead of refusing to start.
var ErrEmbeddingDisabled = errors.New("embedding classifier disabled (no cgo build, or embedding.model_path/vocab_path unset)")
