package prometheus

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllMetricGroups(t *testing.T) {
	r := prometheus.NewRegistry()
	o, err := New(r, Options{Routes: []string{"/work"}, Outputs: []string{"db"}, SecurityRules: []string{"rule"}})
	if err != nil {
		t.Fatal(err)
	}
	e := apilog.Event{Route: "/work", Method: "POST", Status: 429, Duration: time.Second, Panicked: true, Request: apilog.Body{Bytes: 10}, Response: apilog.Body{Bytes: 20}, Profile: &apilog.Profile{Instrumented: true, QueryCount: 20, DuplicateQueries: 10, SQLDuration: 2 * time.Second, Diagnostics: []string{"Possible N+1: investigate"}}, Security: []apilog.SecuritySignal{{RuleID: "rule", Severity: "warning"}}}
	o.RequestStarted(context.Background(), e)
	o.Observe(context.Background(), e)
	o.RequestFinished(context.Background(), e)
	o.ObserveTiming("/work", apilog.Timing{Capture: time.Microsecond, Mask: time.Microsecond, Serialize: time.Microsecond, Enqueue: time.Microsecond, Total: 4 * time.Microsecond})
	o.Skipped("policy")
	o.Pipeline("db", apilog.OutputHealth{Accepted: 50, Delivered: 50, BatchCount: 1, LastBatchSize: 50, LastWriteDuration: time.Millisecond, WorkerRunning: true})
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range families {
		got[f.GetName()] = true
		if f.GetName() == "apilog_api_active_requests" && f.Metric[0].Gauge.GetValue() != 0 {
			t.Fatal("active gauge did not return to zero")
		}
	}
	for _, name := range []string{"apilog_api_requests_total", "apilog_api_duration_seconds", "apilog_api_active_requests", "apilog_api_body_bytes", "apilog_api_slow_requests_total", "apilog_api_exceptions_total", "apilog_api_rate_limited_total", "apilog_profile_query_count", "apilog_profile_sql_duration_seconds", "apilog_profile_duplicate_query_count", "apilog_profile_n_plus_one_hints_total", "apilog_health_queue_entries", "apilog_health_queue_bytes", "apilog_health_worker_running", "apilog_health_events_total", "apilog_health_last_write_seconds", "apilog_health_operation_duration_seconds", "apilog_health_skipped_total", "apilog_health_batches_total", "apilog_health_batch_size", "apilog_health_flush_duration_seconds", "apilog_security_signals_total"} {
		if !got[name] {
			t.Errorf("missing %s", name)
		}
	}
}

func TestCoreLifecycleAndSkippedRequestMetrics(t *testing.T) {
	r := prometheus.NewRegistry()
	o, err := New(r, Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := apilog.DefaultConfig()
	c.Observer = apilog.ObserverGroup{o}
	c.SkipPaths = []string{"/health"}
	l, err := apilog.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Shutdown(context.Background())
	h := httpmw.Middleware(l)(http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		families, err := r.Gather()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range families {
			if f.GetName() == "apilog_api_active_requests" {
				found = true
				if f.Metric[0].Gauge.GetValue() != 1 {
					t.Fatal("in-flight gauge is not one")
				}
			}
		}
		if !found {
			t.Fatal("start hook missing")
		}
		w.WriteHeader(204)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health", nil))
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	api, skip := false, false
	for _, f := range families {
		switch f.GetName() {
		case "apilog_api_active_requests":
			if f.Metric[0].Gauge.GetValue() != 0 {
				t.Fatal("finish hook missing")
			}
		case "apilog_api_requests_total":
			api = true
		case "apilog_health_skipped_total":
			skip = true
		}
	}
	if !api || !skip {
		t.Fatalf("skipped request metrics API=%v skipped=%v", api, skip)
	}
}
