package commonplace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder turns text into a fixed-length vector. The store path embeds
// content on write; search embeds the query. This is the AI-switchable
// seam (plan D2) — ollama-backed in production, faked in tests. Swapping
// to an API embedder later is a new Embedder impl, no caller change.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dim() int
}

// GROWTH HOOK (not MVP): the vector layer is brute-force cosine over
// []float32 blobs (plan D1). When the corpus outgrows a linear scan,
// register the driver's pure-Go vec extension at service open —
//
//	import "github.com/ncruces/go-sqlite3/ext/vec1"
//	db, _ := driver.Open(dsn, vec1.Register)
//
// and back the Searcher seam (search.go) with a `USING vec1` virtual
// table instead of cosineRank. The seam is the only thing that changes.

// OllamaConfig configures the default local embedder. Mirrors
// nexus/runtime/providers/ollama-local.
type OllamaConfig struct {
	URL   string
	Model string
	Dim   int
}

// OllamaEmbedder calls a local ollama's /api/embeddings endpoint.
type OllamaEmbedder struct {
	url    string
	model  string
	dim    int
	client *http.Client
}

// NewOllamaEmbedder validates config and returns the embedder.
func NewOllamaEmbedder(cfg OllamaConfig) (*OllamaEmbedder, error) {
	if cfg.URL == "" || cfg.Model == "" || cfg.Dim <= 0 {
		return nil, fmt.Errorf("commonplace: OllamaConfig requires URL, Model, Dim>0")
	}
	return &OllamaEmbedder{
		url:    cfg.URL,
		model:  cfg.Model,
		dim:    cfg.Dim,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *OllamaEmbedder) Dim() int { return e.dim }

type ollamaEmbedReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}
type ollamaEmbedResp struct {
	Embedding []float32 `json:"embedding"`
}

// Embed posts to {url}/api/embeddings and returns the vector.
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("commonplace: embed: empty text")
	}
	body, err := json.Marshal(ollamaEmbedReq{Model: e.model, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("commonplace: embed marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("commonplace: embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("commonplace: embed: ollama unreachable at %s: %w", e.url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("commonplace: embed read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("commonplace: embed: ollama HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed ollamaEmbedResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("commonplace: embed parse: %w (body: %s)", err, truncate(string(raw), 200))
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("commonplace: embed: empty embedding in response")
	}
	return parsed.Embedding, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
