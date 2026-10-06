package apilog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

type reviewObserver struct{ event Event }

func (o *reviewObserver) Observe(_ context.Context, e Event) { o.event = e }
func (*reviewObserver) Pipeline(string, OutputHealth)        {}

func TestReviewFailedTransformCannotLeakUnsanitizedMutationsToObserver(t *testing.T) {
	for _, fail := range []string{"error", "nil", "panic"} {
		t.Run(fail, func(t *testing.T) {
			observer := &reviewObserver{}
			l, _ := newTestLogger(t, func(c *Config) {
				c.Observer = observer
				c.Transform = func(e Event) (*Event, error) {
					e.RequestHeaders["Authorization"] = []string{"Bearer injected-secret"}
					e.Context["password"] = "injected-secret"
					if fail == "panic" {
						panic("transform fault")
					}
					if fail == "error" {
						return nil, errors.New("transform fault")
					}
					return nil, nil
				}
			})
			l.Emit(context.Background(), sampleEvent())
			data, _ := json.Marshal(observer.event)
			if strings.Contains(string(data), "injected-secret") {
				t.Fatalf("observer received unsanitized mutations after failed transform: %s", data)
			}
		})
	}
}

func TestReviewRejectNaNSamplingRate(t *testing.T) {
	c := DefaultConfig()
	c.Profile.SampleRate = math.NaN()
	l, err := New(c)
	if err == nil {
		_ = l.Shutdown(context.Background())
		t.Fatal("NaN sample rate accepted")
	}
}

func TestReviewCustomFormatSanitizerIsNotReappliedToNormalizedJSON(t *testing.T) {
	count := 0
	l, sink := newTestLogger(t, func(c *Config) {
		c.Sanitizer = func(_ string, data []byte, _ []string) ([]byte, error) {
			count++
			if string(data) != "order=123" {
				return nil, errors.New("expected custom wire format")
			}
			return []byte(`{"order":123}`), nil
		}
		c.Transform = func(e Event) (*Event, error) { e.Name = "enriched"; return &e, nil }
	})
	e := sampleEvent()
	e.Request = Body{State: "complete", ContentType: "text/x-order", Data: []byte("order=123")}
	l.Emit(context.Background(), e)
	flush(t, l)
	got := sink.all()[0]
	if count != 1 || got.Request.State != "complete" || string(got.Request.Data) != `{"order":123}` {
		t.Fatalf("sanitizer reapplied to normalized JSON: calls=%d, body=%+v", count, got.Request)
	}
}

func TestReviewFlushTriggersIndependentOutputsBeforeWaiting(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	fast := &memorySink{}
	l, _ := newTestLogger(t, func(c *Config) {
		c.Queue.FlushInterval = time.Hour
		c.Outputs = []Output{
			{Name: "blocked-first", Kind: "storage", Sink: SinkFunc(func(ctx context.Context, _ []Event) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})},
			{Name: "fast-second", Kind: "storage", Sink: fast},
		}
	})
	l.Emit(context.Background(), sampleEvent())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected slow-output deadline, got %v", err)
	}
	if len(fast.all()) != 1 {
		t.Fatal("blocked first output prevented independent second output from being flushed")
	}
}

func TestReviewCapturePolicyContextSnapshotIsolatedFromSetContext(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	observed := make(chan string, 1)
	l, _ := newTestLogger(t, func(c *Config) {
		c.Policy = func(e Event) (Decision, error) {
			close(entered)
			<-resume
			observed <- e.Context["actor_id"]
			return Decision{}, nil
		}
	})
	ctx, exchange := l.Begin(context.Background(), Event{Context: map[string]string{"actor_id": "original"}})
	done := make(chan struct{})
	go func() { defer close(done); exchange.CaptureAllowed(true) }()
	<-entered
	SetContext(ctx, map[string]string{"actor_id": "updated"})
	close(resume)
	<-done
	if got := <-observed; got != "original" {
		t.Fatalf("policy snapshot aliases live context: got %q, want original", got)
	}
}
