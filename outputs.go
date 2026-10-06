package apilog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
)

// JSONSink emits one JSON event per line. File rotation and writer ownership
// belong to the application. Use a context-aware sink for network transports.
type JSONSink struct {
	Writer io.Writer
	mu     sync.Mutex
}

func (s *JSONSink) WriteBatch(ctx context.Context, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc := json.NewEncoder(s.Writer)
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

type SlogSink struct{ Logger *slog.Logger }

func (s SlogSink) WriteBatch(ctx context.Context, events []Event) error {
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.Logger.LogAttrs(ctx, slog.LevelInfo, "API request", slog.Any("api", e))
	}
	return nil
}
func WithCorrelation(ctx context.Context, logger *slog.Logger) *slog.Logger {
	return logger.With("request_id", RequestID(ctx), "trace_id", TraceID(ctx))
}

type ObserverGroup []Observer

func (g ObserverGroup) RequestStarted(ctx context.Context, e Event) {
	for _, o := range g {
		if obs, ok := o.(RequestObserver); ok {
			func() { defer func() { _ = recover() }(); obs.RequestStarted(ctx, e) }()
		}
	}
}
func (g ObserverGroup) RequestFinished(ctx context.Context, e Event) {
	for _, o := range g {
		if obs, ok := o.(RequestObserver); ok {
			func() { defer func() { _ = recover() }(); obs.RequestFinished(ctx, e) }()
		}
	}
}
func (g ObserverGroup) ObserveTiming(route string, timing Timing) {
	for _, o := range g {
		if obs, ok := o.(TimingObserver); ok {
			func() { defer func() { _ = recover() }(); obs.ObserveTiming(route, timing) }()
		}
	}
}
func (g ObserverGroup) Skipped(reason string) {
	for _, o := range g {
		if obs, ok := o.(SkipObserver); ok {
			func() { defer func() { _ = recover() }(); obs.Skipped(reason) }()
		}
	}
}

func (g ObserverGroup) Observe(ctx context.Context, e Event) {
	for _, o := range g {
		if o != nil {
			func() {
				defer func() { _ = recover() }()
				copied, _, err := cloneEvent(e)
				if err == nil {
					o.Observe(ctx, copied)
				}
			}()
		}
	}
}
func (g ObserverGroup) Pipeline(name string, h OutputHealth) {
	for _, o := range g {
		if o != nil {
			func() { defer func() { _ = recover() }(); o.Pipeline(name, h) }()
		}
	}
}
