package otel

import (
	"context"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	"github.com/vishalanandl177/go-api-logger/integrations/internal/testutil"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExistingSpanCorrelationAndSafeAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	ctx, span := provider.Tracer("application").Start(context.Background(), "existing-request")
	events := &testutil.Observer{}
	conf := apilog.DefaultConfig()
	conf.Observer = apilog.ObserverGroup{Observer{}, events}
	conf.Correlation.Enabled = true
	l, err := apilog.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Shutdown(context.Background())
	h := httpmw.Middleware(l)(Context(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apilog.SetContext(r.Context(), map[string]string{"private": "must-not-be-span-attribute"})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"password":"secret"}`))
	})))
	r := httptest.NewRequest("GET", "/hello", nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), r)
	span.End()
	if len(recorder.Ended()) != 1 {
		t.Fatal("created extra spans")
	}
	e := events.Events()
	if len(e) != 1 || e[0].TraceID != span.SpanContext().TraceID().String() {
		t.Fatal("canonical trace ID missing")
	}
	attrs := recorder.Ended()[0].Attributes()
	found := false
	for _, a := range attrs {
		if string(a.Key) == "apilog.event_id" {
			found = true
		}
		if a.Value.AsString() == "secret" || a.Value.AsString() == "must-not-be-span-attribute" {
			t.Fatal("sensitive field exported")
		}
	}
	if !found {
		t.Fatal("span was not annotated")
	}
	Observer{}.Observe(context.Background(), apilog.Event{Duration: time.Second}) // no active recording span is safe
}
