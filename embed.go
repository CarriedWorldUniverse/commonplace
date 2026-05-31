package commonplace

import "context"

// Embedder turns text into a fixed-length vector. The store path embeds
// content on write; search embeds the query. This is the AI-switchable
// seam (plan D2) — ollama-backed in production, faked in tests.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	// Dim is the fixed vector length this embedder produces. Used to
	// validate stored vectors and size the brute-force cosine scan.
	Dim() int
}
