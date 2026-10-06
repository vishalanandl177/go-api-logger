package apilog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

func reliableTestLogger(t *testing.T, cfg apilog.Config) (*apilog.Logger, http.Handler) {
	t.Helper()
	logger, err := apilog.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := logger.Shutdown(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	handler := httpmw.Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return logger, handler
}

func assertReliableResponse(t *testing.T, handler http.Handler) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/orders", nil))
	if recorder.Code != http.StatusCreated || recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("logging changed application response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func settleReliableLogger(t *testing.T, logger *apilog.Logger) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := logger.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryRecoversAfterSinkFaults(t *testing.T) {
	for _, name := range []string{"returned_error", "panic", "panic_nil", "legacy_panic_nil", "deadline"} {
		t.Run(name, func(t *testing.T) {
			if name == "legacy_panic_nil" {
				t.Setenv("GODEBUG", "panicnil=1")
			}
			var calls, healthy atomic.Int64
			cfg := apilog.DefaultConfig()
			cfg.Queue.BatchSize = 1
			cfg.Queue.WriteTimeout = 10 * time.Millisecond
			cfg.Outputs = []apilog.Output{
				{Name: "recovering", Sink: apilog.SinkFunc(func(ctx context.Context, _ []apilog.Event) error {
					if calls.Add(1) == 1 {
						switch name {
						case "panic":
							panic("private dependency error")
						case "panic_nil", "legacy_panic_nil":
							panic(nil)
						case "deadline":
							<-ctx.Done()
							return ctx.Err()
						default:
							return errors.New("dependency unavailable")
						}
					}
					return nil
				})},
				{Name: "healthy", Sink: apilog.SinkFunc(func(_ context.Context, events []apilog.Event) error {
					healthy.Add(int64(len(events)))
					return nil
				})},
			}
			logger, handler := reliableTestLogger(t, cfg)
			assertReliableResponse(t, handler)
			settleReliableLogger(t, logger)
			failed := logger.Health().Outputs["recovering"]
			if failed.Failed != 1 || failed.Delivered != 0 || !failed.WorkerRunning {
				t.Fatalf("failed batch was not isolated and accounted: %+v", failed)
			}
			assertReliableResponse(t, handler)
			settleReliableLogger(t, logger)
			recovered := logger.Health().Outputs["recovering"]
			if recovered.Accepted != 2 || recovered.Delivered != 1 || recovered.Failed != 1 || recovered.Dropped != 0 || recovered.Queued != 0 || recovered.InFlight != 0 || recovered.QueuedBytes != 0 || !recovered.WorkerRunning {
				t.Fatalf("same logger did not recover for the next batch: %+v", recovered)
			}
			if calls.Load() != 2 || healthy.Load() != 2 {
				t.Fatalf("unexpected retry or cross-output interference: calls=%d healthy=%d", calls.Load(), healthy.Load())
			}
		})
	}
}

type unreliableObserver struct{ pipelineCalls atomic.Int64 }

func (*unreliableObserver) Observe(context.Context, apilog.Event) { panic("observe") }
func (observer *unreliableObserver) Pipeline(string, apilog.OutputHealth) {
	observer.pipelineCalls.Add(1)
	panic("pipeline")
}
func (*unreliableObserver) RequestStarted(context.Context, apilog.Event)  { panic("start") }
func (*unreliableObserver) RequestFinished(context.Context, apilog.Event) { panic("finish") }
func (*unreliableObserver) ObserveTiming(string, apilog.Timing)           { panic("timing") }

func TestObserverPanicsDoNotStopDelivery(t *testing.T) {
	observer := &unreliableObserver{}
	cfg := apilog.DefaultConfig()
	cfg.Observer = observer
	cfg.Queue.BatchSize = 1
	cfg.Outputs = []apilog.Output{{Name: "healthy", Sink: apilog.SinkFunc(func(context.Context, []apilog.Event) error { return nil })}}
	logger, handler := reliableTestLogger(t, cfg)
	for range 3 {
		assertReliableResponse(t, handler)
		settleReliableLogger(t, logger)
	}
	health := logger.Health().Outputs["healthy"]
	if health.Delivered != 3 || health.Failed != 0 || !health.WorkerRunning || observer.pipelineCalls.Load() < 3 {
		t.Fatalf("observer panic interrupted healthy delivery: %+v pipeline=%d", health, observer.pipelineCalls.Load())
	}
}

func TestOutageIsBoundedAndNextBatchesRecover(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int64
	healthyWrites := make(chan struct{}, 32)
	cfg := apilog.DefaultConfig()
	cfg.Queue.BatchSize = 1
	cfg.Queue.Capacity = 4
	cfg.Outputs = []apilog.Output{
		{Name: "recovering", Sink: apilog.SinkFunc(func(ctx context.Context, _ []apilog.Event) error {
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
					return errors.New("outage batch failed")
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})},
		{Name: "healthy", Sink: apilog.SinkFunc(func(context.Context, []apilog.Event) error {
			healthyWrites <- struct{}{}
			return nil
		})},
	}
	logger, handler := reliableTestLogger(t, cfg)
	for index := range 21 {
		assertReliableResponse(t, handler)
		if index == 0 {
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("sink did not enter outage")
			}
		}
		select {
		case <-healthyWrites:
		case <-time.After(time.Second):
			t.Fatal("outage stalled independent healthy sink")
		}
	}
	health := logger.Health().Outputs["recovering"]
	if health.Accepted != 4 || health.Dropped != 17 || health.Queued != 3 || health.InFlight != 1 || health.QueuedBytes <= 0 || health.QueuedBytes > cfg.Queue.MaxBytes || calls.Load() != 1 {
		t.Fatalf("outage exceeded queue budget or spawned parallel writes: %+v calls=%d", health, calls.Load())
	}
	releaseOnce.Do(func() { close(release) })
	settleReliableLogger(t, logger)
	assertReliableResponse(t, handler)
	settleReliableLogger(t, logger)
	health = logger.Health().Outputs["recovering"]
	if health.Accepted != 5 || health.Failed != 1 || health.Delivered != 4 || health.Dropped != 17 || health.QueuedBytes != 0 || !health.WorkerRunning {
		t.Fatalf("outage did not recover with exact loss accounting: %+v", health)
	}
	if healthy := logger.Health().Outputs["healthy"]; healthy.Delivered != 22 || healthy.Failed != 0 || healthy.Dropped != 0 {
		t.Fatalf("outage affected independent healthy sink: %+v", healthy)
	}
}

func TestConcurrentFlushAndShutdownDuringOutage(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	cfg := apilog.DefaultConfig()
	cfg.Queue.BatchSize = 1
	cfg.Queue.Capacity = 4
	cfg.Queue.WriteTimeout = time.Minute
	cfg.Outputs = []apilog.Output{{Name: "unavailable", Sink: apilog.SinkFunc(func(ctx context.Context, _ []apilog.Event) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	})}}
	logger, handler := reliableTestLogger(t, cfg)
	assertReliableResponse(t, handler)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("sink did not begin")
	}
	for range 3 {
		assertReliableResponse(t, handler)
	}
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := logger.Flush(ctx); err != nil {
				t.Errorf("flush did not settle cancelled delivery: %v", err)
			}
		}()
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := logger.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("shutdown: %v", err)
			}
		}()
	}
	wait.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := logger.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	health := logger.Health().Outputs["unavailable"]
	if health.Accepted != 4 || health.Delivered != 0 || health.Failed+health.Dropped != health.Accepted || health.InFlight != 0 || health.Queued != 0 || health.QueuedBytes != 0 || health.WorkerRunning {
		t.Fatalf("shutdown lost or double-counted accepted records: %+v", health)
	}
}

func TestNoncooperativeSinkRemainsBoundedUntilItReturns(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	cfg := apilog.DefaultConfig()
	cfg.Queue.BatchSize = 1
	cfg.Queue.Capacity = 2
	cfg.Queue.WriteTimeout = time.Millisecond
	cfg.Outputs = []apilog.Output{{Name: "stalled", Sink: apilog.SinkFunc(func(context.Context, []apilog.Event) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release // Deliberately broken custom sink: ignores cancellation.
			return errors.New("dependency recovered after timeout")
		}
		return nil
	})}}
	logger, handler := reliableTestLogger(t, cfg)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	assertReliableResponse(t, handler)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("sink did not begin")
	}
	for range 10 {
		assertReliableResponse(t, handler)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := logger.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("flush should time out while the sink ignores cancellation: %v", err)
	}
	health := logger.Health().Outputs["stalled"]
	if health.Accepted != 2 || health.Dropped != 9 || health.InFlight != 1 || health.Queued != 1 || calls.Load() != 1 {
		t.Fatalf("noncooperative sink escaped bounds or spawned replacement calls: %+v calls=%d", health, calls.Load())
	}
	once.Do(func() { close(release) })
	settleReliableLogger(t, logger)
	assertReliableResponse(t, handler)
	settleReliableLogger(t, logger)
	health = logger.Health().Outputs["stalled"]
	if health.Failed != 1 || health.Delivered != 2 || health.Queued != 0 || health.InFlight != 0 || health.QueuedBytes != 0 || !health.WorkerRunning {
		t.Fatalf("sink did not resume once its dependency returned: %+v", health)
	}
}
