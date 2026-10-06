package main

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	apichi "github.com/vishalanandl177/go-api-logger/integrations/chi"
	"github.com/vishalanandl177/go-api-logger/integrations/examples/internal/run"
	"net/http"
)

func main() {
	r := chi.NewRouter()
	r.Use(apichi.Metadata("users"))
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": chi.URLParam(r, "id")})
	})
	run.Serve(r)
}
