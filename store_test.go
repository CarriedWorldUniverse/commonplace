package commonplace

import (
	"context"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := New(context.Background(), Config{DBPath: ":memory:", Embedder: newFakeEmbedder(64)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestStorePersistsAndIndexes(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	e, err := svc.Store(ctx, StoreInput{
		Org: "acme", Owner: "agent:builder",
		Topic: "k8s rollout", Content: "how to roll out a kubernetes deployment safely",
		Visibility: "org", Tags: []string{"ops", "k8s"},
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if e.ID == "" {
		t.Fatal("expected a minted id")
	}
	if e.Visibility != "org" {
		t.Errorf("visibility = %q, want org", e.Visibility)
	}

	// entry row present.
	var n int
	_ = svc.db.QueryRowContext(ctx, `SELECT count(*) FROM entry WHERE id=?`, e.ID).Scan(&n)
	if n != 1 {
		t.Errorf("entry rows = %d, want 1", n)
	}
	// vector row present, correct dim.
	var dim int
	if err := svc.db.QueryRowContext(ctx, `SELECT dim FROM entry_vec WHERE entry_id=?`, e.ID).Scan(&dim); err != nil {
		t.Fatalf("entry_vec missing: %v", err)
	}
	if dim != 64 {
		t.Errorf("stored dim = %d, want 64", dim)
	}
	// FTS row present.
	_ = svc.db.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE entry_id=?`, e.ID).Scan(&n)
	if n != 1 {
		t.Errorf("fts rows = %d, want 1", n)
	}
}

func TestStoreDefaultsVisibilityPrivate(t *testing.T) {
	svc := newTestService(t)
	e, err := svc.Store(context.Background(), StoreInput{
		Org: "acme", Owner: "agent:x", Topic: "t", Content: "c",
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if e.Visibility != "private" {
		t.Errorf("default visibility = %q, want private", e.Visibility)
	}
}

func TestStoreRejectsBadVisibility(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Store(context.Background(), StoreInput{
		Org: "acme", Owner: "a", Topic: "t", Content: "c", Visibility: "public",
	})
	if err == nil {
		t.Fatal("expected error on visibility=public")
	}
}
