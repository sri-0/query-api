package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Ollama calls a local Ollama server's /api/embed endpoint.
type Ollama struct {
	URL   string
	Model string
	dims  int
}

func (o *Ollama) Dims() int { return o.dims }

func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": o.Model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama: status %d", resp.StatusCode)
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama: expected %d embeddings, got %d", len(texts), len(out.Embeddings))
	}
	for _, e := range out.Embeddings {
		if len(e) != o.dims {
			return nil, fmt.Errorf("ollama: model returned %d dims, EMBED_DIMS is %d", len(e), o.dims)
		}
	}
	return out.Embeddings, nil
}
