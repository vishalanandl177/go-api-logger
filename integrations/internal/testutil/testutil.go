// Package testutil contains shared integration conformance helpers.
package testutil

import (
	"context"
	apilog "github.com/vishalanandl177/go-api-logger"
	"sync"
	"testing"
	"time"
)

type Observer struct {
	mu     sync.Mutex
	events []apilog.Event
}

func (o *Observer) Observe(_ context.Context, e apilog.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}
func (*Observer) Pipeline(string, apilog.OutputHealth) {}
func (o *Observer) Events() []apilog.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]apilog.Event(nil), o.events...)
}
func Logger(t testing.TB) (*apilog.Logger, *Observer) {
	t.Helper()
	o := &Observer{}
	c := apilog.DefaultConfig()
	c.Profile.Enabled = true
	c.Observer = o
	c.Correlation.Enabled = true
	l, err := apilog.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := l.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return l, o
}
func Exchange(t testing.TB) (context.Context, func() apilog.Event) {
	t.Helper()
	l, o := Logger(t)
	ctx, x := l.Begin(context.Background(), apilog.Event{ID: "test", Time: time.Now(), Method: "GET", Path: "/test", Route: "/test"})
	return ctx, func() apilog.Event {
		x.Finish(apilog.Event{Status: 200})
		events := o.Events()
		if len(events) != 1 {
			t.Fatalf("got %d events", len(events))
		}
		return events[0]
	}
}
