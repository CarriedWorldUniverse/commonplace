package commonplace

import (
	"net/http"
)

// Handler returns the service's HTTP handler. Task 1 wires only healthz;
// store/search/CRUD routes are added in Tasks 3–6.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"commonplace"}`))
	})
	return mux
}
