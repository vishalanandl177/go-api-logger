package prometheus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apilog "github.com/vishalanandl177/go-api-logger"
)

func TestIndependentlySelectableGroups(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options Options
		want    []string
	}{
		{"api", Options{DisableProfiling: true, DisableLogger: true, DisablePipeline: true, DisableSecurity: true}, []string{"api"}},
		{"profiling", Options{DisableAPI: true, DisableLogger: true, DisablePipeline: true, DisableSecurity: true}, []string{"profiling"}},
		{"logger", Options{DisableAPI: true, DisableProfiling: true, DisablePipeline: true, DisableSecurity: true}, []string{"logger"}},
		{"pipeline", Options{DisableAPI: true, DisableProfiling: true, DisableLogger: true, DisableSecurity: true}, []string{"pipeline"}},
		{"security", Options{DisableAPI: true, DisableProfiling: true, DisableLogger: true, DisablePipeline: true}, []string{"security"}},
		{"health convenience", Options{DisableHealth: true}, []string{"api", "profiling", "security"}},
		{"default", Options{}, []string{"api", "profiling", "logger", "pipeline", "security"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			tc.options.Outputs = []string{"db"}
			observer, err := New(registry, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			observeEveryGroup(observer)
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, family := range families {
				name := family.GetName()
				switch {
				case name == "apilog_health_operation_duration_seconds" || name == "apilog_health_skipped_total":
					got["logger"] = true
				case strings.HasPrefix(name, "apilog_health_"):
					got["pipeline"] = true
				case strings.HasPrefix(name, "apilog_api_"):
					got["api"] = true
				case strings.HasPrefix(name, "apilog_profile_"):
					got["profiling"] = true
				case strings.HasPrefix(name, "apilog_security_"):
					got["security"] = true
				default:
					t.Fatalf("unexpected metric %s", name)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("exported groups=%v want=%v", got, tc.want)
			}
			for _, group := range tc.want {
				if !got[group] {
					t.Errorf("missing group %s", group)
				}
			}
		})
	}
}

func TestLoggerAndPipelineRegisterSeparately(t *testing.T) {
	registry := prometheus.NewRegistry()
	for _, options := range []Options{
		{DisableAPI: true, DisableProfiling: true, DisableSecurity: true, DisablePipeline: true},
		{DisableAPI: true, DisableProfiling: true, DisableSecurity: true, DisableLogger: true},
	} {
		if _, err := New(registry, options); err != nil {
			t.Fatalf("disabled group retained conflicting collectors: %v", err)
		}
	}
}

func observeEveryGroup(observer *Observer) {
	ctx := context.Background()
	event := apilog.Event{Method: "GET", Status: 429, Panicked: true, Duration: time.Second,
		Profile:  &apilog.Profile{Instrumented: true, QueryCount: 2, DuplicateQueries: 1, Diagnostics: []string{"Possible N+1"}},
		Security: []apilog.SecuritySignal{{RuleID: "test", Severity: "warning"}}}
	observer.RequestStarted(ctx, event)
	observer.Observe(ctx, event)
	observer.RequestFinished(ctx, event)
	observer.ObserveTiming("/work", apilog.Timing{Total: time.Microsecond})
	observer.Skipped("policy")
	observer.Pipeline("db", apilog.OutputHealth{Accepted: 1, Delivered: 1, BatchCount: 1, LastBatchSize: 1, LastWriteDuration: time.Millisecond})
}
