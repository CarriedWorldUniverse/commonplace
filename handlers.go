package commonplace

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Handler returns the service's HTTP handler. The /api/ subtree is wrapped
// by withIdentity (plan D6): identity is read from the trusted X-CWB-*
// headers once, 401 is returned centrally for requests that didn't transit
// the gateway, and handlers consume identityFromContext + keep only their
// scope checks.
func (s *Service) Handler() http.Handler {
	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "commonplace"})
	})

	api := http.NewServeMux()
	api.HandleFunc("POST /api/knowledge", s.handleStore)
	api.HandleFunc("GET /api/knowledge/search", s.handleSearch)
	api.HandleFunc("GET /api/knowledge", s.handleList)
	api.HandleFunc("GET /api/knowledge/{id}", s.handleGet)
	api.HandleFunc("PATCH /api/knowledge/{id}", s.handleUpdate)
	api.HandleFunc("DELETE /api/knowledge/{id}", s.handleDelete)
	api.HandleFunc("DELETE /api/org", s.handleOrgPurge)

	root.Handle("/api/", withIdentity(api))
	return root
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

type storeBody struct {
	Topic      string   `json:"topic"`
	Content    string   `json:"content"`
	Visibility string   `json:"visibility"`
	Tags       []string `json:"tags"`
}

func (s *Service) handleStore(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope(scopeWrite) {
		writeErr(w, http.StatusForbidden, "knowledge:write required")
		return
	}
	var body storeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	entry, err := s.Store(r.Context(), StoreInput{
		Org: id.Org, Owner: id.Subject,
		Topic: body.Topic, Content: body.Content,
		Visibility: body.Visibility, Tags: body.Tags,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope(scopeRead) {
		writeErr(w, http.StatusForbidden, "knowledge:read required")
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q required")
		return
	}
	topK := 10
	if v := r.URL.Query().Get("top_k"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}
	hits, err := s.Search(r.Context(), SearchInput{Org: id.Org, Caller: id.Subject, Query: q, TopK: topK})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hits == nil {
		hits = []Hit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden, "not owner"
	default:
		return http.StatusBadRequest, err.Error()
	}
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope(scopeRead) {
		writeErr(w, http.StatusForbidden, "knowledge:read required")
		return
	}
	e, err := s.Get(r.Context(), id.Org, id.Subject, r.PathValue("id"))
	if err != nil {
		code, msg := statusFor(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope(scopeRead) {
		writeErr(w, http.StatusForbidden, "knowledge:read required")
		return
	}
	list, err := s.List(r.Context(), id.Org, id.Subject)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
}

type updateBody struct {
	Topic      *string   `json:"topic"`
	Content    *string   `json:"content"`
	Visibility *string   `json:"visibility"`
	Tags       *[]string `json:"tags"`
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope(scopeWrite) {
		writeErr(w, http.StatusForbidden, "knowledge:write required")
		return
	}
	var body updateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	e, err := s.Update(r.Context(), id.Org, id.Subject, r.PathValue("id"), UpdateInput{
		Topic: body.Topic, Content: body.Content, Visibility: body.Visibility, Tags: body.Tags,
	})
	if err != nil {
		code, msg := statusFor(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope(scopeWrite) {
		writeErr(w, http.StatusForbidden, "knowledge:write required")
		return
	}
	if err := s.Delete(r.Context(), id.Org, id.Subject, r.PathValue("id")); err != nil {
		code, msg := statusFor(err)
		writeErr(w, code, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleOrgPurge(w http.ResponseWriter, r *http.Request) {
	id := identityFromContext(r.Context())
	if !id.hasScope("org:purge") {
		writeErr(w, http.StatusForbidden, "missing scope org:purge")
		return
	}
	n, err := s.DeleteByOrg(r.Context(), id.Org)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "purge failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purged": id.Org, "entries": n})
}
