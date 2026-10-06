package apilog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type memorySink struct {
	mu     sync.Mutex
	events []Event
}

func (m *memorySink) WriteBatch(_ context.Context, e []Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e...)
	return nil
}
func (m *memorySink) all() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event{}, m.events...)
}
func newTestLogger(t *testing.T, change func(*Config)) (*Logger, *memorySink) {
	t.Helper()
	sink := &memorySink{}
	cfg := DefaultConfig()
	cfg.Outputs = []Output{{Name: "memory", Kind: "storage", Sink: sink}}
	if change != nil {
		change(&cfg)
	}
	l, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = l.Shutdown(ctx)
	})
	return l, sink
}
func flush(t *testing.T, l *Logger) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}
func sampleEvent() Event {
	return Event{Method: "POST", URL: "/items?token=secret&page=1", Path: "/items", Route: "/items", Status: 201, RequestHeaders: map[string][]string{"Authorization": {"Bearer private"}}, Request: Body{State: "complete", ContentType: "application/json", Data: json.RawMessage(`{"password":"never-store","n":9007199254740993,"child":[{"API-KEY":"hidden"}]}`)}, Response: Body{State: "complete", ContentType: "application/json", Data: json.RawMessage(`{"ok":true}`)}}
}

func TestSanitizeBeforeEveryOutputAndTransform(t *testing.T) {
	l, sink := newTestLogger(t, func(c *Config) {
		c.Transform = func(e Event) (*Event, error) {
			if strings.Contains(string(e.Request.Data), "never-store") {
				t.Error("transform saw raw secret")
			}
			e.Response.Data = json.RawMessage(`{"token":"injected"}`)
			return &e, nil
		}
	})
	l.Emit(context.Background(), sampleEvent())
	flush(t, l)
	events := sink.all()
	if len(events) != 1 {
		t.Fatal(len(events))
	}
	b, _ := json.Marshal(events[0])
	for _, secret := range []string{"never-store", "hidden", "Bearer private", "injected", "token=secret"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("leaked %s", secret)
		}
	}
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatal("number precision lost")
	}
}
func TestInvalidJSONAndUnsafePartialOmitted(t *testing.T) {
	for _, data := range []string{`{"password":"secret"`, `{"ok":true} {"token":"secret"}`, strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70)} {
		t.Run(data[:min(20, len(data))], func(t *testing.T) {
			l, s := newTestLogger(t, nil)
			e := sampleEvent()
			e.Request.Data = []byte(data)
			l.Emit(context.Background(), e)
			flush(t, l)
			if got := s.all()[0].Request; got.State != "invalid" || len(got.Data) > 0 {
				t.Fatalf("unsafe body: %+v", got)
			}
		})
	}
}
func TestPolicyFailuresFailClosed(t *testing.T) {
	for _, panicHook := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicHook], func(t *testing.T) {
			l, s := newTestLogger(t, func(c *Config) {
				c.Policy = func(Event) (Decision, error) {
					if panicHook {
						panic("secret")
					}
					return Decision{}, errors.New("secret")
				}
			})
			l.Emit(context.Background(), sampleEvent())
			flush(t, l)
			e := s.all()[0]
			if len(e.RequestHeaders) != 0 || len(e.Request.Data) != 0 || len(e.Response.Data) != 0 || !e.ExportDisabled {
				t.Fatal("policy did not fail closed")
			}
		})
	}
}
func TestOutputIsolationOverflowAndShutdown(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	blocked := SinkFunc(func(ctx context.Context, _ []Event) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	l, fast := newTestLogger(t, func(c *Config) {
		c.Queue.Capacity = 2
		c.Queue.BatchSize = 1
		c.Outputs = append(c.Outputs, Output{Name: "blocked", Kind: "export", Sink: blocked})
	})
	l.Emit(context.Background(), sampleEvent())
	<-entered
	for i := 0; i < 10; i++ {
		l.Emit(context.Background(), sampleEvent())
		time.Sleep(time.Millisecond)
	}
	if len(fast.all()) < 2 {
		t.Fatal("fast sink was blocked")
	}
	h := l.Health().Outputs["blocked"]
	if h.Dropped == 0 || h.Queued+h.InFlight > 2 {
		t.Fatalf("unbounded: %+v", h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := l.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(release)
}
func TestByteBudgetAndSinkFailureAccounting(t *testing.T) {
	l, _ := newTestLogger(t, func(c *Config) { c.Queue.MaxBytes = 10 })
	l.Emit(context.Background(), sampleEvent())
	if l.Health().Outputs["memory"].Dropped != 1 {
		t.Fatal("byte cap ignored")
	}
	for _, panics := range []bool{false, true} {
		l, _ := newTestLogger(t, func(c *Config) {
			c.Outputs[0].Sink = SinkFunc(func(context.Context, []Event) error {
				if panics {
					panic("private")
				}
				return errors.New("private")
			})
		})
		l.Emit(context.Background(), sampleEvent())
		flush(t, l)
		if l.Health().Outputs["memory"].Failed != 1 {
			t.Fatal("failure not counted")
		}
	}
}
func TestConcurrentEmitFlushShutdown(t *testing.T) {
	l, _ := newTestLogger(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				l.Emit(context.Background(), sampleEvent())
			}
		}()
	}
	wg.Wait()
	flush(t, l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	l.Emit(ctx, sampleEvent())
	h := l.Health().Outputs["memory"]
	if h.Accepted != h.Delivered || h.Queued != 0 || h.QueuedBytes != 0 || h.WorkerRunning {
		t.Fatalf("bad drain: %+v", h)
	}
}
func TestQueueEventsOwned(t *testing.T) {
	l, s := newTestLogger(t, nil)
	e := sampleEvent()
	l.Emit(context.Background(), e)
	e.Request.Data[0] = 'X'
	e.RequestHeaders["Authorization"][0] = "changed"
	flush(t, l)
	if !json.Valid(s.all()[0].Request.Data) {
		t.Fatal("retained caller body")
	}
}
func TestConfigurationValidation(t *testing.T) {
	for _, change := range []func(*Config){func(c *Config) { c.Queue.Capacity = -1 }, func(c *Config) { c.Profile.SampleRate = 2 }, func(c *Config) { c.TrustedProxies = []string{"bad"} }, func(c *Config) { c.RequestBodyLimit = -1 }, func(c *Config) { c.Outputs = []Output{{Name: "x"}} }, func(c *Config) { c.PathMode = "raw" }} {
		c := DefaultConfig()
		change(&c)
		if l, err := New(c); err == nil {
			_ = l.Shutdown(context.Background())
			t.Fatal("invalid config accepted")
		}
	}
}
func TestProfilesOverlapIsolationAndCompletion(t *testing.T) {
	l, s := newTestLogger(t, func(c *Config) { c.Profile.Enabled = true })
	ctx, x := l.Begin(context.Background(), sampleEvent())
	end1 := StartQuery(ctx, "sql", "SELECT secret FROM t WHERE id=1")
	end2 := StartQuery(ctx, "sql", "SELECT secret FROM t WHERE id=2")
	time.Sleep(time.Millisecond)
	end1(nil)
	end2(nil)
	StartQuery(ctx, "sql", "SELECT unfinished")
	x.Finish(Event{Status: 200})
	flush(t, l)
	p := s.all()[0].Profile
	if p.QueryCount != 3 || p.DuplicateQueries != 1 || !p.Incomplete || p.SQLWallTime > p.SQLDuration {
		t.Fatalf("bad profile %+v", p)
	}
	if ProfileEnabled(ctx) {
		t.Fatal("finished profile remains writable")
	}
	end1(nil)
	ctx2, x2 := l.Begin(context.Background(), sampleEvent())
	StartQuery(SuppressProfiling(ctx2), "sql", "ignored")(nil)
	x2.Finish(Event{Status: 200})
	flush(t, l)
	if s.all()[1].Profile.Instrumented {
		t.Fatal("suppression ignored")
	}
}
func TestPolicyGatesAndRouteCapture(t *testing.T) {
	l, s := newTestLogger(t, func(c *Config) {
		c.Rules = []Rule{{Route: "/private/{id}", Decision: Decision{StripRequest: true, DisableExport: true}}}
	})
	ctx, x := l.Begin(context.Background(), sampleEvent())
	SetRoute(ctx, "/private/{id}", "private", "")
	if x.CaptureAllowed(true) {
		t.Fatal("route capture restriction ignored")
	}
	x.Finish(Event{Status: 200, Request: sampleEvent().Request})
	flush(t, l)
	e := s.all()[0]
	if e.Route != "/private/{id}" || len(e.Request.Data) > 0 || !e.ExportDisabled {
		t.Fatalf("bad policy %+v", e)
	}
}
func FuzzSanitizeJSON(f *testing.F) {
	f.Add([]byte(`{"password":"secret"}`))
	f.Add([]byte(`{"nested":[1,2]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		clean, err := SanitizeJSON(data, nil)
		if err == nil && !json.Valid(clean) {
			t.Fatal("invalid sanitizer output")
		}
	})
}
func BenchmarkEmit(b *testing.B) {
	for _, metadata := range []bool{false, true} {
		name := "body"
		if metadata {
			name = "metadata"
		}
		b.Run(name, func(b *testing.B) {
			cfg := DefaultConfig()
			cfg.MetadataOnly = metadata
			cfg.Outputs = []Output{{Name: "discard", Kind: "storage", Sink: SinkFunc(func(context.Context, []Event) error { return nil })}}
			l, err := New(cfg)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := l.Shutdown(ctx); err != nil {
					b.Fatal(err)
				}
				health := l.Health().Outputs["discard"]
				b.ReportMetric(float64(health.Delivered)/float64(b.N), "delivered/op")
				b.ReportMetric(float64(health.Dropped)/float64(b.N), "dropped/op")
				if health.Failed != 0 {
					b.Fatalf("discard sink failed %d events", health.Failed)
				}
			})
			e := sampleEvent()
			b.ReportAllocs()
			for b.Loop() {
				l.Emit(context.Background(), e)
			}
		})
	}
}
