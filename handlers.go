package commonplace

import (
	"encoding/json"
	"net/http"
)

// Handler returns the service's HTTP handler.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "commonplace"})
	})
	mux.HandleFunc("POST /api/knowledge", s.handleStore)
	return mux
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
	id := identityFromRequest(r)
	if id.Subject == "" || id.Org == "" {
		writeErr(w, http.StatusUnauthorized, "missing identity")
		return
	}
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
