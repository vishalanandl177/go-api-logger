package prometheus

import (
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	apilog "github.com/vishalanandl177/go-api-logger"
)

// Handler serves an application-owned registry after authorization. Mount it on
// the application's existing server. Scrapes are excluded from API log outputs
// even when outer capture middleware wraps the endpoint.
func Handler(gatherer prometheus.Gatherer, authorize func(*http.Request) bool) (http.Handler, error) {
	if gatherer == nil || authorize == nil {
		return nil, errors.New("apilog/prometheus: gatherer and authorization are required")
	}
	metrics := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apilog.SkipRequest(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			w = headWriter{w}
		}
		if !allowed(authorize, r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		metrics.ServeHTTP(w, r)
	}), nil
}

func allowed(authorize func(*http.Request) bool, r *http.Request) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return authorize(r)
}

type headWriter struct{ http.ResponseWriter }

func (w headWriter) Write(p []byte) (int, error) { return len(p), nil }
