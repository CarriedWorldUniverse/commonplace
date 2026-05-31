package commonplace

import (
	"context"
	"testing"
)

// fakeEmbedder is the deterministic test embedder. Defined in
// embed_test.go in Task 2; for Task 1 we declare a local minimal one.
type schemaTestEmbedder struct{}

func (schemaTestEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, 8), nil
}
func (schemaTestEmbedder) Dim() int { return 8 }

func TestNewAppliesSchemaIdempotently(t *testing.T) {
	ctx := context.Background()
	cfg := Config{DBPath: ":memory:", Embedder: schemaTestEmbedder{}}
	svc, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	// Re-applying schema on the live DB must be a no-op (idempotent).
	if err := applySchema(ctx, svc.db); err != nil {
		t.Fatalf("applySchema second run: %v", err)
	}

	// entry, entry_vec, entry_fts must all exist.
	for _, tbl := range []string{"entry", "entry_vec", "entry_fts"} {
		var name string
		err := svc.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE name = ?`, tbl).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %q to exist: %v", tbl, err)
		}
	}
}
