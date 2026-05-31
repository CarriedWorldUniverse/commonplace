package commonplace

import (
	"context"
	"testing"
)

func TestHybridSearchSemanticRecall(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	// Store an entry phrased one way.
	target, err := svc.Store(ctx, StoreInput{
		Org: "acme", Owner: "agent:a", Visibility: "org",
		Topic:   "kubernetes deployment rollout",
		Content: "safely roll out a new container image to a kubernetes cluster with health checks",
	})
	if err != nil {
		t.Fatalf("store target: %v", err)
	}
	// Store a distractor on an unrelated concept.
	if _, err := svc.Store(ctx, StoreInput{
		Org: "acme", Owner: "agent:a", Visibility: "org",
		Topic: "invoice formatting", Content: "how to format a PDF invoice for accounting",
	}); err != nil {
		t.Fatalf("store distractor: %v", err)
	}

	// Query with DIFFERENT wording, sharing the concept token "kubernetes"
	// but not the exact phrase — semantic+keyword fusion should surface
	// the target ranked first.
	res, err := svc.Search(ctx, SearchInput{
		Org: "acme", Caller: "agent:a",
		Query: "updating a kubernetes service without downtime",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one hit")
	}
	if res[0].Entry.ID != target.ID {
		t.Fatalf("top hit = %q (%q), want target %q", res[0].Entry.ID, res[0].Entry.Topic, target.ID)
	}
	if res[0].Score <= 0 {
		t.Errorf("fused score = %v, want > 0", res[0].Score)
	}
}

func TestSearchScopesToOrgAndVisibility(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	// Another org's entry must never surface.
	if _, err := svc.Store(ctx, StoreInput{Org: "other", Owner: "x", Visibility: "org",
		Topic: "kubernetes", Content: "kubernetes in another org"}); err != nil {
		t.Fatal(err)
	}
	// Another owner's PRIVATE entry in my org must not surface.
	if _, err := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "private",
		Topic: "kubernetes", Content: "private kubernetes notes by b"}); err != nil {
		t.Fatal(err)
	}
	// My own private entry SHOULD surface.
	mine, err := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Visibility: "private",
		Topic: "kubernetes", Content: "my private kubernetes notes"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.Search(ctx, SearchInput{Org: "acme", Caller: "agent:a", Query: "kubernetes", TopK: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range res {
		if h.Entry.Org != "acme" {
			t.Errorf("cross-org leak: %q", h.Entry.Org)
		}
		if h.Entry.Visibility == "private" && h.Entry.Owner != "agent:a" {
			t.Errorf("foreign private leak: owner %q", h.Entry.Owner)
		}
	}
	var sawMine bool
	for _, h := range res {
		if h.Entry.ID == mine.ID {
			sawMine = true
		}
	}
	if !sawMine {
		t.Error("expected my own private entry to surface")
	}
}
