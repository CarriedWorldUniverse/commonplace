package commonplace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedderCallsEmbeddingsEndpoint(t *testing.T) {
	var gotPath, gotModel, gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, gotPrompt = body.Model, body.Prompt
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": []float32{0.1, 0.2, 0.3, 0.4},
		})
	}))
	defer srv.Close()

	e, err := NewOllamaEmbedder(OllamaConfig{URL: srv.URL, Model: "test-model", Dim: 4})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}
	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotPath != "/api/embeddings" {
		t.Errorf("path = %q, want /api/embeddings", gotPath)
	}
	if gotModel != "test-model" || gotPrompt != "hello world" {
		t.Errorf("model/prompt = %q/%q", gotModel, gotPrompt)
	}
	if len(vec) != 4 || vec[0] != 0.1 {
		t.Errorf("vec = %v", vec)
	}
	if e.Dim() != 4 {
		t.Errorf("Dim = %d, want 4", e.Dim())
	}
}

func TestOllamaEmbedderEmptyTextErrors(t *testing.T) {
	e, _ := NewOllamaEmbedder(OllamaConfig{URL: "http://unused", Model: "m", Dim: 4})
	if _, err := e.Embed(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty text")
	}
}
