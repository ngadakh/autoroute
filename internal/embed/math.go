package embed

import "math"

// Dim is the embedding width for all-MiniLM-L6-v2.
const Dim = 384

// Cosine similarity of two L2-normalised vectors (a plain dot product).
func Cosine(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
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
