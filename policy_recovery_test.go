package apilog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPolicyFailureContainsSensitiveDataAndRecovers(t *testing.T) {
	for _, fault := range []string{"error", "panic", "nil-panic"} {
		t.Run(fault, func(t *testing.T) {
			// Exercise the supported legacy nil-panic behavior as well as the
			// ordinary panic path. Do not parallelize environment changes.
			if fault == "nil-panic" {
				t.Setenv("GODEBUG", "panicnil=1")
			}
			var storage, exported []Event
			cfg := DefaultConfig()
			cfg.Outputs = []Output{
				{Name: "storage", Kind: "storage", Sink: SinkFunc(func(_ context.Context, events []Event) error { storage = append(storage, events...); return nil })},
				{Name: "export", Kind: "export", Sink: SinkFunc(func(_ context.Context, events []Event) error { exported = append(exported, events...); return nil })},
			}
			fail := true
			cfg.Policy = func(Event) (Decision, error) {
				if fail {
					switch fault {
					case "error":
						return Decision{}, errors.New("temporary policy fault")
					case "panic":
						panic("temporary policy fault")
					default:
						panic(nil)
					}
				}
				return Decision{}, nil
			}
			logger, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			defer func() {
				if err := logger.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			}()
			makeEvent := func() Event {
				return Event{Method: "POST", Path: "/items", URL: "/items?customer=private-value", Status: 200, RequestHeaders: map[string][]string{"X-Customer": {"private-value"}}, ResponseHeaders: map[string][]string{"X-Customer": {"private-value"}}, Request: Body{State: "complete", ContentType: "application/json", Data: []byte(`{"customer":"private-value"}`)}, Response: Body{State: "complete", ContentType: "application/json", Data: []byte(`{"customer":"private-value"}`)}}
			}
			logger.Emit(ctx, makeEvent())
			if err := logger.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if len(storage) != 1 || len(exported) != 0 {
				t.Fatalf("policy fault delivered storage=%d export=%d", len(storage), len(exported))
			}
			e := storage[0]
			if len(e.Request.Data) != 0 || len(e.Response.Data) != 0 || len(e.RequestHeaders) != 0 || len(e.ResponseHeaders) != 0 || strings.Contains(e.URL, "private-value") {
				t.Fatalf("policy fault retained sensitive fields: %+v", e)
			}
			fail = false
			logger.Emit(ctx, makeEvent())
			if err := logger.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if len(storage) != 2 || len(exported) != 1 || len(exported[0].Request.Data) == 0 {
				t.Fatal("later healthy policy did not recover")
			}
		})
	}
}
