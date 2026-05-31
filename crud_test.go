package commonplace

import (
	"context"
	"testing"
)

func TestGetByIDRespectsScope(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	priv, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "private", Topic: "t", Content: "c"})
	shared, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "org", Topic: "t2", Content: "c2"})

	// agent:a may read the org-shared one.
	if _, err := svc.Get(ctx, "acme", "agent:a", shared.ID); err != nil {
		t.Errorf("Get org-shared: %v", err)
	}
	// agent:a may NOT read agent:b's private one.
	if _, err := svc.Get(ctx, "acme", "agent:a", priv.ID); err == nil {
		t.Error("expected not-found on foreign private")
	}
	// cross-org: never.
	if _, err := svc.Get(ctx, "other", "agent:a", shared.ID); err == nil {
		t.Error("expected not-found cross-org")
	}
}

func TestListReturnsVisible(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	_, _ = svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Visibility: "private", Topic: "mine", Content: "x"})
	_, _ = svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "org", Topic: "shared", Content: "y"})
	_, _ = svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "private", Topic: "hidden", Content: "z"})
	list, err := svc.List(ctx, "acme", "agent:a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2 (mine + shared)", len(list))
	}
}

func TestUpdateReembeds(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	e, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Topic: "t", Content: "original content"})

	var before []byte
	_ = svc.db.QueryRowContext(ctx, `SELECT embedding FROM entry_vec WHERE entry_id=?`, e.ID).Scan(&before)

	newContent := "completely different replacement text"
	upd, err := svc.Update(ctx, "acme", "agent:a", e.ID, UpdateInput{Content: &newContent})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Content != newContent {
		t.Errorf("content = %q", upd.Content)
	}
	var after []byte
	_ = svc.db.QueryRowContext(ctx, `SELECT embedding FROM entry_vec WHERE entry_id=?`, e.ID).Scan(&after)
	if string(before) == string(after) {
		t.Error("expected embedding to change after content update (re-embed)")
	}
}

func TestUpdateRejectsNonOwner(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	e, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Visibility: "org", Topic: "t", Content: "c"})
	nc := "x"
	if _, err := svc.Update(ctx, "acme", "agent:b", e.ID, UpdateInput{Content: &nc}); err == nil {
		t.Error("expected non-owner update to fail")
	}
}

func TestDeleteRemovesAllIndexes(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	e, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Topic: "t", Content: "c"})
	if err := svc.Delete(ctx, "acme", "agent:a", e.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM entry WHERE id=?`,
		`SELECT count(*) FROM entry_vec WHERE entry_id=?`,
		`SELECT count(*) FROM entry_fts WHERE entry_id=?`,
	} {
		var n int
		_ = svc.db.QueryRowContext(ctx, q, e.ID).Scan(&n)
		if n != 0 {
			t.Errorf("%q left %d rows", q, n)
		}
	}
}
