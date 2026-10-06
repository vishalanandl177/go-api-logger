// Package chi enriches outer HTTP capture with chi route metadata.
package chi

import (
	"github.com/go-chi/chi/v5"
	apilog "github.com/vishalanandl177/go-api-logger"
	"net/http"
)

// Metadata reads the final route pattern after routing has completed.
func Metadata(group string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if route := chi.RouteContext(r.Context()); route != nil {
					apilog.SetRoute(r.Context(), route.RoutePattern(), "", group)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
