package main

import "testing"

func TestHarnessScenarios(t *testing.T) {
	r, err := run(config{Requests: 120, Concurrency: 2, Warmup: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Results) != 6 {
		t.Fatal("incomplete scenario set")
	}
	for _, result := range r.Results {
		if result.RequestFailures != 0 || !result.ObservedWithinQueueLimits || result.P99Microseconds <= 0 || result.RequestsPerSecond <= 0 {
			t.Fatalf("invalid scenario %+v", result)
		}
		if result.Scenario == "outage" && result.Failed == 0 {
			t.Fatal("outage was not exercised")
		}
		if result.Scenario == "sqlite" && result.Delivered != uint64(result.Requests) {
			t.Fatal("SQLite did not persist all harness requests")
		}
	}
}
