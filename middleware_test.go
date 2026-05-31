package commonplace

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentityMiddlewareInjectsContext(t *testing.T) {
	var got Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = identityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := withIdentity(next)

	req := httptest.NewRequest(http.MethodGet, "/api/knowledge", nil)
	req.Header.Set("X-CWB-Org", "acme")
	req.Header.Set("X-CWB-Subject", "agent:a")
	req.Header.Set("X-CWB-Kind", "agent")
	req.Header.Set("X-CWB-Scopes", "knowledge:read knowledge:write")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got.Org != "acme" || got.Subject != "agent:a" || got.Kind != "agent" {
		t.Errorf("identity = %+v", got)
	}
	if !got.hasScope("knowledge:write") {
		t.Error("missing scope")
	}
}

func TestIdentityMiddlewareRejectsMissing(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := withIdentity(next)
	req := httptest.NewRequest(http.MethodGet, "/api/knowledge", nil) // no headers
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}
