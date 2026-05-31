package commonplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrgPurge(t *testing.T) {
	svc := newTestService(t)
	rw := "knowledge:read knowledge:write"

	// Store two entries in org "o1".
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		svc.Handler().ServeHTTP(rec, authReq(http.MethodPost, "/api/knowledge",
			`{"topic":"t","content":"c","visibility":"org"}`, "o1", "builder", rw))
		if rec.Code != http.StatusCreated {
			t.Fatalf("store entry %d: status=%d body=%s", i, rec.Code, rec.Body.String())
		}
	}

	// DELETE /api/org without org:purge scope → 403.
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodDelete, "/api/org", "", "o1", "sys", "knowledge:write"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing-scope: status=%d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// DELETE /api/org with org:purge scope → 200, entries gone.
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodDelete, "/api/org", "", "o1", "sys", "org:purge"))
	if rec.Code != http.StatusOK {
		t.Fatalf("purge: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("purge: invalid JSON response: %v", err)
	}
	if resp["purged"] != "o1" {
		t.Errorf("purge: purged=%v, want o1", resp["purged"])
	}

	// List for o1 should return 0 entries.
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge", "", "o1", "builder", rw))
	if rec.Code != http.StatusOK {
		t.Fatalf("list after purge: status=%d", rec.Code)
	}
	var listResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list: invalid JSON: %v", err)
	}
	entries, _ := listResp["entries"].([]any)
	if len(entries) != 0 {
		t.Errorf("list after purge: got %d entries, want 0", len(entries))
	}

	// Second purge → 200 (idempotent).
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodDelete, "/api/org", "", "o1", "sys", "org:purge"))
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent purge: status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
