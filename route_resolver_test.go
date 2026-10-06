package apilog

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouteResolverReadsLateMetadataOutsideExchangeLock(t *testing.T) {
	l, sink := newTestLogger(t, func(c *Config) {
		c.Rules = []Rule{{Route: "/private/{id}", Decision: Decision{StripRequest: true}}}
	})
	ctx, x := l.Begin(context.Background(), Event{RequestID: "request-1", Method: "POST"})
	pattern := ""
	finish := SetRouteResolver(ctx, func() (string, string, string) {
		if RequestID(ctx) != "request-1" {
			t.Error("resolver cannot read shared exchange metadata")
		}
		return pattern, "update", "private"
	})
	pattern = "/private/{id}"
	if x.CaptureAllowed(true) {
		t.Fatal("capture allowed before current route policy was evaluated")
	}
	if !x.CaptureAllowed(false) {
		t.Fatal("request-only restriction also suppressed response")
	}
	finish()
	x.Finish(Event{Status: 204})
	flush(t, l)
	e := sink.all()[0]
	if e.Route != pattern || e.Name != "update" || e.Group != "private" {
		t.Fatalf("final metadata was lost: %+v", e)
	}
}

func TestRouteResolverFinalizersPreserveLatestAuthority(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested", true: "explicit"}[explicit], func(t *testing.T) {
			l, sink := newTestLogger(t, nil)
			ctx, x := l.Begin(context.Background(), Event{Method: "GET"})
			outerCalls, innerCalls := 0, 0
			outer := SetRouteResolver(ctx, func() (string, string, string) {
				outerCalls++
				return "/outer", "", "outer"
			})
			inner := SetRouteResolver(ctx, func() (string, string, string) {
				innerCalls++
				return "/inner", "", "inner"
			})
			want, wantCalls := "inner", 1
			if explicit {
				SetRoute(ctx, "/explicit", "", "explicit")
				want, wantCalls = "explicit", 0
			}
			inner()
			outer()
			inner()
			outer()
			x.Finish(Event{Status: 204})
			flush(t, l)
			e := sink.all()[0]
			if e.Route != "/"+want || e.Group != want || outerCalls != 0 || innerCalls != wantCalls {
				t.Fatalf("stale finalizer changed authority: route=%q group=%q outer=%d inner=%d", e.Route, e.Group, outerCalls, innerCalls)
			}
		})
	}
}

func TestRouteResolverFinalizerWaitsBeforePooledStateReuse(t *testing.T) {
	l, _ := newTestLogger(t, nil)
	ctx, x := l.Begin(context.Background(), Event{})
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	pattern := "/private/{id}"
	finalize := SetRouteResolver(ctx, func() (string, string, string) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return pattern, "", "private"
	})
	readDone := make(chan struct{})
	go func() { defer close(readDone); x.CaptureAllowed(true) }()
	<-entered
	finalizeStarted, finalized := make(chan struct{}), make(chan struct{})
	go func() { close(finalizeStarted); finalize(); close(finalized) }()
	<-finalizeStarted
	select {
	case <-finalized:
		t.Fatal("finalizer returned while an active reader still held pooled state")
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	select {
	case <-finalized:
	case <-time.After(time.Second):
		t.Fatal("finalization did not complete after active reader returned")
	}
	<-readDone
	pattern = "/reused-for-another-request"
	x.CaptureAllowed(false)
	finalize()
	if calls.Load() != 2 {
		t.Fatalf("detached resolver read reused pooled state: calls=%d", calls.Load())
	}
}

func TestRouteResolverFailureCannotRetainPayloadOrInspectionSamples(t *testing.T) {
	for _, failAt := range []string{"capture", "finalize"} {
		t.Run(failAt, func(t *testing.T) {
			observer := &reviewObserver{}
			l, sink := newTestLogger(t, func(c *Config) {
				c.Observer = observer
				c.Security.Enabled = true
				c.Security.InspectRequest, c.Security.InspectResponse = true, true
				c.Security.Rules = []string{"DRFSEC-006", "DRFSEC-011"}
			})
			ctx, x := l.Begin(context.Background(), Event{Method: "POST"})
			fail := failAt == "capture"
			finalize := SetRouteResolver(ctx, func() (string, string, string) {
				if fail {
					panic("router failure")
				}
				return "/private", "", "private"
			})
			allowed := x.CaptureAllowed(true)
			if allowed == fail {
				t.Fatalf("unexpected initial capture permission: allowed=%v fail=%v", allowed, fail)
			}
			x.InspectSamples([]byte(`<script>`), []byte(`password`))
			fail = true
			finalize()
			SetRouteResolver(ctx, func() (string, string, string) { return "/public", "", "public" })()
			if x.CaptureAllowed(false) {
				t.Fatal("a later resolver cleared the failure restriction")
			}
			x.Finish(Event{Status: 200,
				Request:  Body{State: "complete", Data: []byte(`{"input":"<script>"}`)},
				Response: Body{State: "complete", Data: []byte(`{"password":"secret"}`)},
			})
			flush(t, l)
			if len(sink.all()) != 0 || len(observer.event.Request.Data) != 0 || len(observer.event.Response.Data) != 0 || len(observer.event.Security) != 0 {
				t.Fatalf("failed resolver retained payload or security inspection: stored=%d event=%+v", len(sink.all()), observer.event)
			}
		})
	}
}
