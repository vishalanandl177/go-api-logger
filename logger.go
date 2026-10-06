package apilog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

type Logger struct {
	config   Config
	outputs  []*outputWorker
	closed   atomic.Bool
	skipped  atomic.Uint64
	security *securityDetector
	mu       sync.Mutex
}
type queuedEvent struct {
	event Event
	size  int
}
type outputWorker struct {
	logger         *Logger
	output         Output
	mu             sync.Mutex
	queue          []queuedEvent
	health         OutputHealth
	settled        uint64
	closing, force bool
	wake           chan struct{}
	changed        chan struct{}
	done           chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
}

func New(config Config) (*Logger, error) {
	config = cloneConfig(config)
	d := DefaultConfig()
	if config.Queue.Capacity == 0 {
		config.Queue.Capacity = d.Queue.Capacity
	}
	if config.Queue.MaxBytes == 0 {
		config.Queue.MaxBytes = d.Queue.MaxBytes
	}
	if config.Queue.BatchSize == 0 {
		config.Queue.BatchSize = d.Queue.BatchSize
	}
	if config.Queue.FlushInterval == 0 {
		config.Queue.FlushInterval = d.Queue.FlushInterval
	}
	if config.Queue.WriteTimeout == 0 {
		config.Queue.WriteTimeout = d.Queue.WriteTimeout
	}
	if config.RequestBodyLimit == 0 {
		config.RequestBodyLimit = d.RequestBodyLimit
	}
	if config.ResponseBodyLimit == 0 {
		config.ResponseBodyLimit = d.ResponseBodyLimit
	}
	if config.ContentTypes == nil {
		config.ContentTypes = d.ContentTypes
	}
	if config.PathMode == "" {
		config.PathMode = d.PathMode
	}
	if config.Profile.MaxQueries == 0 {
		config.Profile.MaxQueries = d.Profile.MaxQueries
	}
	if config.SlowThreshold == 0 {
		config.SlowThreshold = d.SlowThreshold
	}
	if config.Correlation.RequestIDHeaders == nil {
		config.Correlation.RequestIDHeaders = d.Correlation.RequestIDHeaders
	}
	if config.Correlation.TraceIDHeaders == nil {
		config.Correlation.TraceIDHeaders = d.Correlation.TraceIDHeaders
	}
	if config.Security.SampleBytes == 0 {
		config.Security.SampleBytes = d.Security.SampleBytes
	}
	if config.Security.MaxActors == 0 {
		config.Security.MaxActors = d.Security.MaxActors
	}
	if config.Security.Window == 0 {
		config.Security.Window = d.Security.Window
	}
	if config.Queue.Capacity < 1 || config.Queue.MaxBytes < 1 || config.Queue.BatchSize < 1 || config.Queue.BatchSize > config.Queue.Capacity || config.Queue.FlushInterval < 0 || config.Queue.WriteTimeout < 0 {
		return nil, errors.New("apilog: invalid queue limits")
	}
	if config.RequestBodyLimit < 1 || config.ResponseBodyLimit < 1 || config.RequestBodyLimit > 16<<20 || config.ResponseBodyLimit > 16<<20 {
		return nil, errors.New("apilog: body limits must be 1 byte to 16 MiB")
	}
	if math.IsNaN(config.Profile.SampleRate) || config.Profile.SampleRate < 0 || config.Profile.SampleRate > 1 || config.Profile.MaxQueries < 1 {
		return nil, errors.New("apilog: invalid profiling configuration")
	}
	if config.PathMode != "full" && config.PathMode != "path" && config.PathMode != "absolute" {
		return nil, errors.New("apilog: invalid path mode")
	}
	for _, p := range config.TrustedProxies {
		if _, err := netip.ParsePrefix(p); err != nil {
			return nil, fmt.Errorf("apilog: invalid trusted proxy prefix")
		}
	}
	seen := map[string]bool{}
	for i := range config.Outputs {
		o := &config.Outputs[i]
		if o.Name == "" || seen[o.Name] || o.Sink == nil {
			return nil, errors.New("apilog: output names must be unique and sinks non-nil")
		}
		seen[o.Name] = true
		if o.Kind == "" {
			o.Kind = "export"
		}
		if o.Kind != "storage" && o.Kind != "export" {
			return nil, errors.New("apilog: invalid output kind")
		}
	}
	l := &Logger{config: config}
	var err error
	l.security, err = newSecurityDetector(config.Security)
	if err != nil {
		return nil, err
	}
	for _, output := range config.Outputs {
		ctx, cancel := context.WithCancel(context.Background())
		w := &outputWorker{logger: l, output: output, wake: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{}), ctx: ctx, cancel: cancel, health: OutputHealth{WorkerRunning: true}}
		l.outputs = append(l.outputs, w)
		go w.run()
	}
	return l, nil
}

// Config returns a copy for adapters. Treat hook functions as immutable.
func (l *Logger) Config() Config { return cloneConfig(l.config) }
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("apilog: random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func cloneEvent(e Event) (Event, int, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return Event{}, 0, err
	}
	var owned Event
	err = json.Unmarshal(b, &owned)
	return owned, len(b), err
}

// Emit accepts an owned exchange, applies policies and sanitizes it before any
// observer or sink is invoked. It performs no sink I/O on the calling goroutine.
func (l *Logger) Emit(ctx context.Context, e Event) {
	if l.closed.Load() {
		return
	}
	started := time.Now()
	timing := Timing{Capture: e.CaptureDuration}
	defer func() {
		timing.Total = time.Since(started) + timing.Capture
		if observer, ok := l.config.Observer.(TimingObserver); ok {
			func() { defer func() { _ = recover() }(); observer.ObserveTiming(e.Route, timing) }()
		}
	}()
	d := l.decision(e, true)
	if requestSkipped(ctx) {
		d.Skip = true
	}
	if len(l.outputs) == 0 {
		d.StripHeaders = true
		d.StripRequest = true
		d.StripResponse = true
	}
	if d.Skip {
		d.StripHeaders = true
		d.StripRequest = true
		d.StripResponse = true
		d.StripQuery = true
		l.skip("policy")
	}
	if e.ID == "" {
		e.ID = newID()
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	maskStart := time.Now()
	e = l.sanitize(e, d)
	timing.Mask += time.Since(maskStart)
	e.ExportDisabled = d.DisableExport || e.ExportDisabled
	if !d.Skip && l.config.Transform != nil {
		exportDisabled := e.ExportDisabled
		var out *Event
		var err error
		transformInput, _, cloneErr := cloneEvent(e)
		func() {
			defer func() {
				if recover() != nil {
					err = errors.New("transform failed")
				}
			}()
			if cloneErr != nil {
				err = cloneErr
				return
			}
			out, err = l.config.Transform(transformInput)
		}()
		if err != nil || out == nil {
			l.skip("transform")
			d.Skip = true
		} else {
			maskStart = time.Now()
			e = l.sanitize(*out, d)
			timing.Mask += time.Since(maskStart)
			e.ExportDisabled = d.DisableExport || exportDisabled || out.ExportDisabled
		}
	}
	if e.Profile != nil {
		e.Profile.LoggerOverhead += time.Since(started)
	}
	if l.config.Observer != nil {
		serializeStart := time.Now()
		owned, _, err := cloneEvent(e)
		timing.Serialize += time.Since(serializeStart)
		if err == nil {
			func() { defer func() { _ = recover() }(); l.config.Observer.Observe(ctx, owned) }()
		}
	}
	if d.Skip || l.closed.Load() {
		return
	}
	for _, w := range l.outputs {
		if (w.output.Kind == "storage" && d.DisableStorage) || (w.output.Kind == "export" && e.ExportDisabled) {
			continue
		}
		serializeStart := time.Now()
		owned, size, err := cloneEvent(e)
		timing.Serialize += time.Since(serializeStart)
		enqueueStart := time.Now()
		w.mu.Lock()
		if w.closing {
			w.mu.Unlock()
			continue
		}
		if err != nil || len(w.queue)+w.health.InFlight >= l.config.Queue.Capacity || w.health.QueuedBytes+size > l.config.Queue.MaxBytes {
			w.health.Dropped++
			w.notifyLocked()
			w.mu.Unlock()
			timing.Enqueue += time.Since(enqueueStart)
			w.report()
			continue
		}
		w.queue = append(w.queue, queuedEvent{owned, size})
		w.health.Queued = len(w.queue)
		w.health.QueuedBytes += size
		w.health.Accepted++
		wake := len(w.queue) >= l.config.Queue.BatchSize
		w.mu.Unlock()
		timing.Enqueue += time.Since(enqueueStart)
		if wake {
			w.signal()
		}
		w.report()
	}
}
func (l *Logger) skip(reason string) {
	l.skipped.Add(1)
	if observer, ok := l.config.Observer.(SkipObserver); ok {
		func() { defer func() { _ = recover() }(); observer.Skipped(reason) }()
	}
}
func (w *outputWorker) notifyLocked() { close(w.changed); w.changed = make(chan struct{}) }
func (w *outputWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *outputWorker) snapshot() OutputHealth { w.mu.Lock(); defer w.mu.Unlock(); return w.health }
func (w *outputWorker) report() {
	if w.logger.config.Observer != nil {
		func() {
			defer func() { _ = recover() }()
			w.logger.config.Observer.Pipeline(w.output.Name, w.snapshot())
		}()
	}
}
func (w *outputWorker) run() {
	ticker := time.NewTicker(w.logger.config.Queue.FlushInterval)
	defer ticker.Stop()
	defer close(w.done)
	defer func() { w.mu.Lock(); w.health.WorkerRunning = false; w.notifyLocked(); w.mu.Unlock(); w.report() }()
	for {
		select {
		case <-w.ctx.Done():
			w.discard()
			return
		case <-ticker.C:
			w.mu.Lock()
			w.force = true
			w.mu.Unlock()
		case <-w.wake:
		}
		for {
			w.mu.Lock()
			if len(w.queue) == 0 {
				w.force = false
				stop := w.closing
				w.mu.Unlock()
				if stop {
					return
				}
				break
			}
			if !w.force && !w.closing && len(w.queue) < w.logger.config.Queue.BatchSize {
				w.mu.Unlock()
				break
			}
			n := min(len(w.queue), w.logger.config.Queue.BatchSize)
			batch := make([]Event, n)
			size := 0
			for i, item := range w.queue[:n] {
				batch[i] = item.event
				size += item.size
			}
			copy(w.queue, w.queue[n:])
			clear(w.queue[len(w.queue)-n:])
			w.queue = w.queue[:len(w.queue)-n]
			w.health.Queued = len(w.queue)
			w.health.InFlight = n
			w.mu.Unlock()
			ctx, cancel := context.WithTimeout(w.ctx, w.logger.config.Queue.WriteTimeout)
			start := time.Now()
			err := func() (err error) {
				defer func() {
					if recover() != nil {
						err = errors.New("apilog: sink panic")
					}
				}()
				return w.output.Sink.WriteBatch(ctx, batch)
			}()
			cancel()
			w.mu.Lock()
			w.health.LastWriteDuration = time.Since(start)
			w.health.LastBatchSize = n
			w.health.BatchCount++
			w.health.InFlight = 0
			w.health.QueuedBytes -= size
			w.settled += uint64(n)
			if err != nil {
				w.health.Failed += uint64(n)
			} else {
				w.health.Delivered += uint64(n)
			}
			w.notifyLocked()
			w.mu.Unlock()
			w.report()
			if w.ctx.Err() != nil {
				w.discard()
				return
			}
		}
	}
}
func (w *outputWorker) discard() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.health.Dropped += uint64(len(w.queue))
	w.settled += uint64(len(w.queue))
	for _, q := range w.queue {
		w.health.QueuedBytes -= q.size
	}
	w.queue = nil
	w.health.Queued = 0
	w.notifyLocked()
}

func (l *Logger) Health() Health {
	h := Health{Closed: l.closed.Load(), Skipped: l.skipped.Load(), Outputs: make(map[string]OutputHealth)}
	for _, w := range l.outputs {
		h.Outputs[w.output.Name] = w.snapshot()
	}
	return h
}
func (l *Logger) Flush(ctx context.Context) error {
	targets := make([]uint64, len(l.outputs))
	for i, w := range l.outputs {
		w.mu.Lock()
		targets[i] = w.health.Accepted
		w.force = true
		w.mu.Unlock()
		w.signal()
	}
	for i, w := range l.outputs {
		for {
			w.mu.Lock()
			done := w.settled >= targets[i]
			changed := w.changed
			w.mu.Unlock()
			if done {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			}
		}
	}
	return nil
}
func (l *Logger) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	l.closed.Store(true)
	for _, w := range l.outputs {
		w.mu.Lock()
		w.closing = true
		w.mu.Unlock()
		w.signal()
	}
	l.mu.Unlock()
	for _, w := range l.outputs {
		select {
		case <-w.done:
		case <-ctx.Done():
			for _, o := range l.outputs {
				o.cancel()
				o.discard()
			}
			return ctx.Err()
		}
	}
	for _, w := range l.outputs {
		w.cancel()
	}
	return nil
}
