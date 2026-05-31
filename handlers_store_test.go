package commonplace

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostKnowledgeStores(t *testing.T) {
	svc := newTestService(t)
	body := `{"topic":"k8s rollout","content":"roll out a kubernetes deployment","visibility":"org","tags":["ops"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge", strings.NewReader(body))
	req.Header.Set("X-CWB-Org", "acme")
	req.Header.Set("X-CWB-Subject", "agent:builder")
	req.Header.Set("X-CWB-Scopes", "knowledge:read knowledge:write")
	rec := httptest.NewRecorder()

	svc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"owner":"agent:builder"`) {
		t.Errorf("response missing owner: %s", rec.Body.String())
	}
}

func TestPostKnowledgeRequiresWriteScope(t *testing.T) {
	svc := newTestService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge", strings.NewReader(`{"topic":"t","content":"c"}`))
	req.Header.Set("X-CWB-Org", "acme")
	req.Header.Set("X-CWB-Subject", "agent:builder")
	req.Header.Set("X-CWB-Scopes", "knowledge:read") // no write
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
