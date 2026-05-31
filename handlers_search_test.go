package commonplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func storeViaHTTP(t *testing.T, svc *Service, org, subj, topic, content, vis string) {
	t.Helper()
	b := `{"topic":"` + topic + `","content":"` + content + `","visibility":"` + vis + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge", strings.NewReader(b))
	req.Header.Set("X-CWB-Org", org)
	req.Header.Set("X-CWB-Subject", subj)
	req.Header.Set("X-CWB-Scopes", "knowledge:read knowledge:write")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("store status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetSearchReturnsRankedHits(t *testing.T) {
	svc := newTestService(t)
	storeViaHTTP(t, svc, "acme", "agent:a", "kubernetes rollout", "roll out a kubernetes deployment", "org")
	storeViaHTTP(t, svc, "acme", "agent:a", "invoice", "format a pdf invoice", "org")

	u := "/api/knowledge/search?q=" + url.QueryEscape("update a kubernetes service") + "&top_k=5"
	req := httptest.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("X-CWB-Org", "acme")
	req.Header.Set("X-CWB-Subject", "agent:a")
	req.Header.Set("X-CWB-Scopes", "knowledge:read")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Hits []Hit `json:"hits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hits) == 0 || !strings.Contains(out.Hits[0].Entry.Topic, "kubernetes") {
		t.Fatalf("expected kubernetes entry top; got %+v", out.Hits)
	}
}

func TestGetSearchRequiresReadScope(t *testing.T) {
	svc := newTestService(t)
	req := httptest.NewRequest(http.MethodGet, "/api/knowledge/search?q=x", nil)
	req.Header.Set("X-CWB-Org", "acme")
	req.Header.Set("X-CWB-Subject", "agent:a")
	// no scopes
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}
