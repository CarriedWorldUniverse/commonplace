package commonplace

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
)

// fakeEmbedder is a deterministic bag-of-words embedder for tests. Each
// token is hashed into a bucket and accumulated, then the vector is
// L2-normalized. Texts sharing tokens (even reworded around shared
// concept words) get higher cosine similarity than disjoint texts —
// enough to exercise the hybrid-search semantic path without a live model.
type fakeEmbedder struct{ dim int }

func newFakeEmbedder(dim int) *fakeEmbedder { return &fakeEmbedder{dim: dim} }

func (f *fakeEmbedder) Dim() int { return f.dim }

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, f.dim)
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		tok = strings.Trim(tok, ".,;:!?\"'()")
		if tok == "" {
			continue
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		v[h.Sum32()%uint32(f.dim)] += 1
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range v {
			v[i] /= n
		}
	}
	return v, nil
}
