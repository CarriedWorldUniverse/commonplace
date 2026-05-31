package commonplace

import (
	"math"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	v := []float32{1.5, -2.25, 0, 3.125}
	blob := encodeVector(v)
	got, err := decodeVector(blob)
	if err != nil {
		t.Fatalf("decodeVector: %v", err)
	}
	if len(got) != len(v) {
		t.Fatalf("len = %d, want %d", len(got), len(v))
	}
	for i := range v {
		if got[i] != v[i] {
			t.Errorf("got[%d] = %v, want %v", i, got[i], v[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	if s := cosine(a, a); math.Abs(float64(s)-1) > 1e-6 {
		t.Errorf("cosine(a,a) = %v, want 1", s)
	}
	b := []float32{0, 1, 0}
	if s := cosine(a, b); math.Abs(float64(s)) > 1e-6 {
		t.Errorf("cosine(a,b) = %v, want 0", s)
	}
	c := []float32{2, 0, 0} // same direction as a, different magnitude
	if s := cosine(a, c); math.Abs(float64(s)-1) > 1e-6 {
		t.Errorf("cosine(a,c) = %v, want 1", s)
	}
}
