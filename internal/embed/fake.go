package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Fake is a deterministic bag-of-words hashing embedder. It has no semantic
// understanding but similar sentences produce similar vectors, which is enough
// for local development and tests.
type Fake struct{ dims int }

func (f *Fake) Dims() int { return f.dims }

func (f *Fake) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.one(t)
	}
	return out, nil
}

func (f *Fake) one(text string) []float32 {
	v := make([]float32, f.dims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		h := fnv.New64a()
		h.Write([]byte(w))
		sum := h.Sum64()
		for k := 0; k < 3; k++ { // three sparse dimensions per token
			idx := int((sum >> (k * 16)) % uint64(f.dims))
			sign := float32(1)
			if (sum>>(k*16+15))&1 == 1 {
				sign = -1
			}
			v[idx] += sign
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x * x)
	}
	if norm == 0 {
		v[0] = 1
		return v
	}
	n := float32(math.Sqrt(norm))
	for i := range v {
		v[i] /= n
	}
	return v
}
