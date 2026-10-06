package apilog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCapturePolicyFailureClearsAndRejectsInspectionSamples(t *testing.T) {
	config := DefaultConfig()
	config.Security.Enabled = true
	config.Security.InspectRequest = true
	config.Security.InspectResponse = true
	config.Policy = func(Event) (Decision, error) { return Decision{}, errors.New("temporary policy fault") }
	logger, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := logger.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	_, exchange := logger.Begin(context.Background(), Event{Method: "POST", Path: "/test"})
	defer exchange.Finish(Event{Status: 200})
	exchange.InspectSamples([]byte("old-request-sample"), []byte("old-response-sample"))
	if len(exchange.requestSample) == 0 || len(exchange.responseSample) == 0 {
		t.Fatal("sample setup failed")
	}
	if exchange.CaptureAllowed(true) {
		t.Fatal("failed policy allowed capture")
	}
	if !exchange.policyFailed || exchange.requestSample != nil || exchange.responseSample != nil {
		t.Fatal("failed policy retained earlier inspection samples")
	}
	exchange.InspectSamples([]byte("new-request-sample"), []byte("new-response-sample"))
	if exchange.requestSample != nil || exchange.responseSample != nil {
		t.Fatal("failed exchange accepted new inspection samples")
	}
}
