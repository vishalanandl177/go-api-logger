// Package chi enriches outer HTTP capture with chi route metadata.
package chi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	apilog "github.com/vishalanandl177/go-api-logger"
)

// Metadata supplies the current route pattern before body capture and snapshots
// it after routing completes. Install with Router.Use, inside outer HTTP capture.
func Metadata(group string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := chi.RouteContext(r.Context())
			defer apilog.SetRouteResolver(r.Context(), func() (string, string, string) {
				return route.RoutePattern(), "", group
			})()
			next.ServeHTTP(w, r)
		})
	}
}
