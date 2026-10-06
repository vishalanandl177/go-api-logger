// Package exampletest verifies the observable contract of runnable examples.
package exampletest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

// Check exercises the documented requests and checks delivered, sanitized events.
func Check(t *testing.T, router http.Handler, getRoute, postRoute string) {
	t.Helper()
	var output bytes.Buffer
	config := apilog.DefaultConfig()
	config.Outputs = []apilog.Output{{Name: "test", Kind: "export", Sink: &apilog.JSONSink{Writer: &output}}}
	logger, err := apilog.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := logger.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	handler := httpmw.Middleware(logger)(router)
	for _, request := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/users/42", "", http.StatusOK},
		{http.MethodPost, "/users", `{"name":"Ada","token":"demo-token-not-a-real-credential"}`, http.StatusCreated},
		{http.MethodPost, "/users", `{"`, http.StatusBadRequest},
		{http.MethodGet, "/missing", "", http.StatusNotFound},
	} {
		r := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		if request.body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != request.status {
			t.Fatalf("%s %s: status %d, want %d", request.method, request.path, w.Code, request.status)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := logger.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "demo-token-not-a-real-credential") {
		t.Fatal("fictional secret reached output without masking")
	}
	decoder := json.NewDecoder(&output)
	var events []apilog.Event
	for decoder.More() {
		var event apilog.Event
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events; want one per request", len(events))
	}
	if events[0].Route != getRoute || events[1].Route != postRoute {
		t.Fatalf("routes: GET %q, POST %q", events[0].Route, events[1].Route)
	}
	if events[1].Request.State != "complete" || !strings.Contains(string(events[1].Request.Data), "***FILTERED***") {
		t.Fatalf("POST capture: %+v", events[1].Request)
	}
	if events[2].Request.State != "invalid" || len(events[2].Request.Data) != 0 {
		t.Fatalf("malformed JSON capture: %+v", events[2].Request)
	}
}
