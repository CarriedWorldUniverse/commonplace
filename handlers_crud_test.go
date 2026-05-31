package commonplace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authReq(method, path, body, org, subj, scopes string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.Header.Set("X-CWB-Org", org)
	r.Header.Set("X-CWB-Subject", subj)
	r.Header.Set("X-CWB-Scopes", scopes)
	return r
}

func TestCRUDHandlers(t *testing.T) {
	svc := newTestService(t)
	rw := "knowledge:read knowledge:write"

	// create
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodPost, "/api/knowledge",
		`{"topic":"t","content":"c","visibility":"org"}`, "acme", "agent:a", rw))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
	}
	var created Entry
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// get
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge/"+created.ID, "", "acme", "agent:a", rw))
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d", rec.Code)
	}

	// list
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge", "", "acme", "agent:a", rw))
	if rec.Code != http.StatusOK {
		t.Fatalf("list %d", rec.Code)
	}

	// patch
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodPatch, "/api/knowledge/"+created.ID,
		`{"content":"updated"}`, "acme", "agent:a", rw))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch %d: %s", rec.Code, rec.Body.String())
	}

	// delete
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodDelete, "/api/knowledge/"+created.ID, "", "acme", "agent:a", rw))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete %d", rec.Code)
	}

	// get after delete -> 404
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge/"+created.ID, "", "acme", "agent:a", rw))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete %d, want 404", rec.Code)
	}
}
