// Package embed turns text into vectors for semantic search.
package embed

import (
	"context"
	"fmt"
)

// Embedder produces one vector per input text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dims() int
}

// New selects an implementation by name.
func New(provider string, dims int, ollamaURL, ollamaModel string) (Embedder, error) {
	switch provider {
	case "fake":
		return &Fake{dims: dims}, nil
	case "ollama":
		return &Ollama{URL: ollamaURL, Model: ollamaModel, dims: dims}, nil
	default:
		return nil, fmt.Errorf("unknown EMBED_PROVIDER %q", provider)
	}
}
