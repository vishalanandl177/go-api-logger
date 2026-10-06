// Package sentry attaches bounded API metadata to a request-local Sentry hub.
// It never captures or sends an event.
package sentry

import (
	"context"
	"github.com/getsentry/sentry-go"
	apilog "github.com/vishalanandl177/go-api-logger"
	"net/http"
)

// Context creates a request-local hub, preserving the application's client and
// scope without mutating the global hub. Put it outside HTTP capture.
func Context(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := sentry.GetHubFromContext(r.Context())
		if hub == nil {
			hub = sentry.CurrentHub()
		}
		ctx := sentry.SetHubOnContext(r.Context(), hub.Clone())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AttachCorrelation is useful immediately before an application captures an
// error. The final observer runs after the handler, which is too late for an
// event the application has already sent.
func AttachCorrelation(ctx context.Context) {
	if hub := sentry.GetHubFromContext(ctx); hub != nil {
		hub.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetContext("apilog", sentry.Context{"request_id": apilog.RequestID(ctx), "trace_id": apilog.TraceID(ctx)})
		})
	}
}

type Observer struct{}

func (Observer) Observe(ctx context.Context, e apilog.Event) {
	if hub := sentry.GetHubFromContext(ctx); hub != nil {
		hub.ConfigureScope(func(scope *sentry.Scope) {
			data := sentry.Context{"event_id": e.ID, "request_id": e.RequestID, "trace_id": e.TraceID, "route": e.Route, "status": e.Status, "duration_ns": int64(e.Duration)}
			if e.Profile != nil {
				data["query_count"] = e.Profile.QueryCount
				data["profile_incomplete"] = e.Profile.Incomplete
			}
			scope.SetContext("apilog", data)
		})
	}
}
func (Observer) Pipeline(string, apilog.OutputHealth) {}
