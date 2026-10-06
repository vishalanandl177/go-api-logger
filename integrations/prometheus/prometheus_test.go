package prometheus

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	apilog "github.com/vishalanandl177/go-api-logger"
	"testing"
)

func TestBoundedLabelsAndMonotonicHealth(t *testing.T) {
	r := prometheus.NewRegistry()
	o, err := New(r, Options{Routes: []string{"/users/{id}"}, Outputs: []string{"store"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		o.Observe(context.Background(), apilog.Event{Route: fmt.Sprintf("/user/%d", i), Method: fmt.Sprint(i), Status: 200, Security: []apilog.SecuritySignal{{RuleID: fmt.Sprint(i), Severity: fmt.Sprint(i)}}})
		o.Pipeline(fmt.Sprint(i), apilog.OutputHealth{Accepted: 1})
	}
	o.Pipeline("store", apilog.OutputHealth{Accepted: 10, Delivered: 8})
	o.Pipeline("store", apilog.OutputHealth{Accepted: 8, Delivered: 7}) // stale callback
	o.Pipeline("store", apilog.OutputHealth{Accepted: 11, Delivered: 9})
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "apilog_api_requests_total" || f.GetName() == "apilog_security_signals_total" {
			if len(f.Metric) != 1 {
				t.Fatalf("unbounded %s: %d", f.GetName(), len(f.Metric))
			}
		}
		if f.GetName() == "apilog_health_events_total" {
			for _, m := range f.Metric {
				for _, l := range m.Label {
					if l.GetName() == "outcome" && l.GetValue() == "accepted" && m.GetCounter().GetValue() != 11 {
						t.Fatal("stale callback inflated accepted count")
					}
				}
			}
		}
	}
}
func TestGroupsAndRegistrationFailure(t *testing.T) {
	r := prometheus.NewRegistry()
	if _, err := New(r, Options{DisableAPI: true, DisableProfiling: true, DisableHealth: true, DisableSecurity: true}); err != nil {
		t.Fatal(err)
	}
	families, err := r.Gather()
	if err != nil || len(families) != 0 {
		t.Fatal("disabled groups exported metrics")
	}
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("accepted missing registry")
	}
	if _, err := New(r, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(r, Options{}); err == nil {
		t.Fatal("duplicate registration accepted")
	}
}
