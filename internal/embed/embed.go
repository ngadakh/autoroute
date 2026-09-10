package embed

import (
	"fmt"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Dim is the embedding width for all-MiniLM-L6-v2.
const Dim = 384

var (
	initOnce sync.Once
	initErr  error
)

// InitRuntime points ONNX Runtime at the shared library fetched by
// scripts/setup-spike.sh and initialises the global environment. Safe to call
// multiple times; only the first call does work.
func InitRuntime(sharedLibPath string) error {
	initOnce.Do(func() {
		ort.SetSharedLibraryPath(sharedLibPath)
		initErr = ort.InitializeEnvironment()
	})
	return initErr
}

// Embedder runs a sentence-transformer ONNX model in-process.
type Embedder struct {
	tok        *Tokenizer
	session    *ort.DynamicAdvancedSession
	inputNames []string
	outName    string
	mu         sync.Mutex // ORT sessions are not guaranteed goroutine-safe
}

// NewEmbedder loads the model and tokenizer. InitRuntime must have been called
// first. The expected graph signature suits the Xenova/all-MiniLM-L6-v2 export
// (input_ids, attention_mask, optionally token_type_ids -> last_hidden_state).
func NewEmbedder(modelPath, vocabPath string, maxSeqLen int) (*Embedder, error) {
	tok, err := LoadTokenizer(vocabPath, maxSeqLen)
	if err != nil {
		return nil, err
	}

	inInfo, outInfo, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model: %w", err)
	}
	if len(outInfo) == 0 {
		return nil, fmt.Errorf("model has no outputs")
	}
	have := names(inInfo)

	want := []string{"input_ids", "attention_mask", "token_type_ids"}
	if !contains(have, want[0]) || !contains(have, want[1]) {
		return nil, fmt.Errorf("model inputs %v missing input_ids/attention_mask", have)
	}
	if !contains(have, want[2]) {
		want = want[:2] // some exports drop token_type_ids
	}
	outName := outInfo[0].Name

	sess, err := ort.NewDynamicAdvancedSession(modelPath, want, []string{outName}, nil)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &Embedder{tok: tok, session: sess, inputNames: want, outName: outName}, nil
}

// ModelOutputName reports the graph output the embedder reads (for diagnostics).
func (e *Embedder) ModelOutputName() string { return e.outName }

// Close releases the ONNX session.
func (e *Embedder) Close() error { return e.session.Destroy() }

// Embed returns an L2-normalised, mean-pooled sentence embedding for text.
func (e *Embedder) Embed(text string) ([]float32, error) {
	enc := e.tok.Encode(text)
	seqLen := int64(enc.Len())
	shape := ort.NewShape(1, seqLen)

	idsT, err := ort.NewTensor(shape, enc.InputIDs)
	if err != nil {
		return nil, err
	}
	defer idsT.Destroy()

	maskT, err := ort.NewTensor(shape, enc.AttentionMask)
	if err != nil {
		return nil, err
	}
	defer maskT.Destroy()

	inputs := []ort.Value{idsT, maskT}
	if len(e.inputNames) == 3 {
		typesT, err := ort.NewTensor(shape, enc.TokenTypeIDs)
		if err != nil {
			return nil, err
		}
		defer typesT.Destroy()
		inputs = append(inputs, typesT)
	}

	outT, err := ort.NewEmptyTensor[float32](ort.NewShape(1, seqLen, Dim))
	if err != nil {
		return nil, err
	}
	defer outT.Destroy()

	e.mu.Lock()
	err = e.session.Run(inputs, []ort.Value{outT})
	e.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("session run: %w", err)
	}

	return meanPoolNormalise(outT.GetData(), enc.AttentionMask, Dim), nil
}

// meanPoolNormalise collapses [seq, dim] token vectors to one [dim] vector,
// weighting by the attention mask, then L2-normalises so cosine similarity is a
// plain dot product.
func meanPoolNormalise(flat []float32, mask []int64, dim int) []float32 {
	seq := len(mask)
	out := make([]float32, dim)
	var count float32
	for i := 0; i < seq; i++ {
		if mask[i] == 0 {
			continue
		}
		count++
		base := i * dim
		for d := 0; d < dim; d++ {
			out[d] += flat[base+d]
		}
	}
	if count > 0 {
		for d := range out {
			out[d] /= count
		}
	}

	var norm float64
	for _, v := range out {
		norm += float64(v) * float64(v)
	}
	if norm = math.Sqrt(norm); norm > 0 {
		inv := float32(1 / norm)
		for d := range out {
			out[d] *= inv
		}
	}
	return out
}

// Cosine similarity of two L2-normalised vectors (a plain dot product).
func Cosine(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func names(info []ort.InputOutputInfo) []string {
	out := make([]string, len(info))
	for i, x := range info {
		out[i] = x.Name
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
