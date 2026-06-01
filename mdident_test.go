package commonplace

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestIdentityFromMD_Valid(t *testing.T) {
	md := metadata.Pairs(
		"cwb-subject", "agent:builder",
		"cwb-org", "acme",
		"cwb-kind", "agent",
		"cwb-scopes", "knowledge:read knowledge:write",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	id, ok := identityFromMD(ctx)
	if !ok {
		t.Fatal("expected ok=true for valid metadata")
	}
	if id.Subject != "agent:builder" {
		t.Errorf("Subject = %q, want agent:builder", id.Subject)
	}
	if id.Org != "acme" {
		t.Errorf("Org = %q, want acme", id.Org)
	}
	if id.Kind != "agent" {
		t.Errorf("Kind = %q, want agent", id.Kind)
	}
	if len(id.Scopes) != 2 {
		t.Errorf("Scopes len = %d, want 2: %v", len(id.Scopes), id.Scopes)
	}
	if !id.hasScope("knowledge:read") {
		t.Error("expected scope knowledge:read")
	}
	if !id.hasScope("knowledge:write") {
		t.Error("expected scope knowledge:write")
	}
}

func TestIdentityFromMD_MissingOrg(t *testing.T) {
	md := metadata.Pairs(
		"cwb-subject", "agent:x",
		// no cwb-org
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	_, ok := identityFromMD(ctx)
	if ok {
		t.Fatal("expected ok=false when org is missing")
	}
}

func TestIdentityFromMD_MissingSubject(t *testing.T) {
	md := metadata.Pairs(
		"cwb-org", "acme",
		// no cwb-subject
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	_, ok := identityFromMD(ctx)
	if ok {
		t.Fatal("expected ok=false when subject is missing")
	}
}

func TestIdentityFromMD_NoMetadata(t *testing.T) {
	_, ok := identityFromMD(context.Background())
	if ok {
		t.Fatal("expected ok=false when no metadata in context")
	}
}
