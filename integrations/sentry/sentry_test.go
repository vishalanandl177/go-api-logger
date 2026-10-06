package sentry

import (
	"context"
	"github.com/getsentry/sentry-go"
	apilog "github.com/vishalanandl177/go-api-logger"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestHubIsolationAndSafeContext(t *testing.T) {
	base := sentry.NewHub(nil, sentry.NewScope())
	base.ConfigureScope(func(s *sentry.Scope) { s.SetTag("application", "existing") })
	var hubs []*sentry.Hub
	h := Context(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := sentry.GetHubFromContext(r.Context())
		hubs = append(hubs, hub)
		Observer{}.Observe(r.Context(), apilog.Event{ID: "event", RequestID: "request", Status: 500, Context: map[string]string{"secret": "hidden"}})
	}))
	for range 2 {
		r := httptest.NewRequest("GET", "/", nil).WithContext(sentry.SetHubOnContext(context.Background(), base))
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	if hubs[0] == hubs[1] || hubs[0] == base {
		t.Fatal("request reused a mutable hub")
	}
	baseEvent := base.Scope().ApplyToEvent(sentry.NewEvent(), &sentry.EventHint{}, nil)
	if _, ok := baseEvent.Contexts["apilog"]; ok {
		t.Fatal("global scope was modified")
	}
	e := hubs[0].Scope().ApplyToEvent(sentry.NewEvent(), &sentry.EventHint{}, nil)
	if e.Tags["application"] != "existing" || e.Contexts["apilog"]["event_id"] != "event" {
		t.Fatal("existing scope or local metadata missing")
	}
	if _, ok := e.Contexts["apilog"]["secret"]; ok {
		t.Fatal("arbitrary context exported")
	}
}
