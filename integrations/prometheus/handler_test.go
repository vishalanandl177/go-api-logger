package prometheus

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

func TestProtectedMetricsHandler(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "application_requests_total", Help: "Example count."})
	registry.MustRegister(counter)
	counter.Inc()
	if _, err := Handler(registry, nil); err == nil {
		t.Fatal("nil authorization accepted")
	}
	if _, err := Handler(nil, func(*http.Request) bool { return true }); err == nil {
		t.Fatal("nil gatherer accepted")
	}
	handler, err := Handler(registry, func(r *http.Request) bool { return r.Header.Get("Authorization") == "test-authorized" })
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, auth string
		code         int
	}{{"GET", "", 403}, {"GET", "test-authorized", 200}, {"HEAD", "test-authorized", 200}, {"POST", "test-authorized", 405}} {
		r := httptest.NewRequest(tc.method, "/internal/metrics", nil)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s status=%d", tc.method, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing safe response headers")
		}
		if tc.method == "GET" && tc.code == 200 && !strings.Contains(w.Body.String(), "application_requests_total 1") {
			t.Fatal("registry was not served")
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned body")
		}
		if tc.code == 405 && w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal("Allow header missing")
		}
	}
	panics, err := Handler(registry, func(*http.Request) bool { panic("private authorization detail") })
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	panics.ServeHTTP(w, httptest.NewRequest("GET", "/internal/metrics", nil))
	if w.Code != 403 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("authorization panic did not fail closed")
	}
}

func TestMetricsHandlerExcludesItsOwnLogOutput(t *testing.T) {
	r := prometheus.NewRegistry()
	o, err := New(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	config := apilog.DefaultConfig()
	config.Observer = apilog.ObserverGroup{o}
	config.Outputs = []apilog.Output{{Name: "test", Kind: "export", Sink: &apilog.JSONSink{Writer: &output}}}
	l, err := apilog.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Shutdown(context.Background())
	h, err := Handler(r, func(*http.Request) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	httpmw.Middleware(l)(h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/arbitrary-metrics-mount", nil))
	if err := l.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("metrics request was written to log output")
	}
	// Direct Emit has no corresponding RequestStarted call and must not lower
	// the in-flight gauge. Request lifecycle hooks belong only to Begin/Finish.
	l.Emit(context.Background(), apilog.Event{Method: "POST", Status: 204})
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "apilog_api_active_requests" {
			for _, m := range f.Metric {
				if m.Gauge.GetValue() != 0 {
					t.Fatalf("unbalanced lifecycle gauge: %v", m.Gauge.GetValue())
				}
			}
		}
	}
}
