// Package otel annotates the application's existing active span. It creates no
// provider, exporter, span or network activity.
package otel

import (
	"context"
	apilog "github.com/vishalanandl177/go-api-logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"net/http"
)

type Observer struct{}

func (Observer) Observe(ctx context.Context, e apilog.Event) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("apilog.event_id", e.ID), attribute.String("apilog.request_id", e.RequestID),
		attribute.Int("apilog.status_code", e.Status), attribute.Int64("apilog.duration_ns", int64(e.Duration)),
		attribute.Bool("apilog.panicked", e.Panicked),
	}
	if e.Route != "" {
		attrs = append(attrs, attribute.String("apilog.route", e.Route))
	}
	if e.Profile != nil {
		attrs = append(attrs, attribute.Bool("apilog.sql.instrumented", e.Profile.Instrumented), attribute.Int("apilog.sql.query_count", e.Profile.QueryCount), attribute.Int64("apilog.sql.duration_ns", int64(e.Profile.SQLDuration)), attribute.Bool("apilog.profile.incomplete", e.Profile.Incomplete))
	}
	span.SetAttributes(attrs...)
}
func (Observer) Pipeline(string, apilog.OutputHealth) {}

// Context copies the active trace ID into safe application context metadata.
// Put it inside HTTP capture and outside your application router.
func Context(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
			apilog.SetTraceID(r.Context(), sc.TraceID().String())
		}
		next.ServeHTTP(w, r)
	})
}
