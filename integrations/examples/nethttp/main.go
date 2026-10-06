package main

import (
	"encoding/json"
	"net/http"

	"github.com/vishalanandl177/go-api-logger/integrations/examples/internal/run"
)

func main() {
	run.Serve(newRouter())
}

func newRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": r.PathValue("id")})
	})
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]bool{"created": true})
	})
	return mux
}
