// Package prometheus exports API, profiling, pipeline health and security metrics.
// Metric labels are limited to explicit allowlists and fixed enumerations.
package prometheus

import (
	"context"
	"errors"
	"github.com/prometheus/client_golang/prometheus"
	apilog "github.com/vishalanandl177/go-api-logger"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Routes, Outputs, SecurityRules                               []string
	DisableAPI, DisableProfiling, DisableHealth, DisableSecurity bool
	SlowThreshold                                                time.Duration
}
type Observer struct {
	routes, outputs, rules map[string]bool
	requests               *prometheus.CounterVec
	duration               *prometheus.HistogramVec
	active                 *prometheus.GaugeVec
	sizes                  *prometheus.HistogramVec
	slow                   *prometheus.CounterVec
	exceptions             *prometheus.CounterVec
	rateLimited            *prometheus.CounterVec
	slowThreshold          time.Duration
	queries                *prometheus.HistogramVec
	sqlDuration            *prometheus.HistogramVec
	duplicates             *prometheus.HistogramVec
	nplusone               *prometheus.CounterVec
	signals                *prometheus.CounterVec
	queue                  *prometheus.GaugeVec
	queueBytes             *prometheus.GaugeVec
	workers                *prometheus.GaugeVec
	pipeline               *prometheus.CounterVec
	writeDuration          *prometheus.GaugeVec
	timing                 *prometheus.HistogramVec
	skipped                *prometheus.CounterVec
	batchSize              *prometheus.HistogramVec
	flushDuration          *prometheus.HistogramVec
	batches                *prometheus.CounterVec
	mu                     sync.Mutex
	last                   map[string]apilog.OutputHealth
}

func allow(values []string) (map[string]bool, error) {
	if len(values) > 256 {
		return nil, errors.New("apilog/prometheus: at most 256 values per label allowlist")
	}
	m := make(map[string]bool, len(values))
	for _, s := range values {
		if s == "" || len(s) > 200 || strings.ContainsAny(s, "\r\n\x00") {
			return nil, errors.New("apilog/prometheus: invalid label allowlist")
		}
		m[s] = true
	}
	return m, nil
}

// New registers only on the supplied registry, and rolls back on registration
// failure. Unknown routes/rules collapse to "other". Unknown outputs are ignored
// so arbitrary output names cannot grow health metric cardinality.
func New(reg prometheus.Registerer, opts Options) (*Observer, error) {
	if reg == nil {
		return nil, errors.New("apilog/prometheus: registry is required")
	}
	routes, err := allow(opts.Routes)
	if err != nil {
		return nil, err
	}
	outputs, err := allow(opts.Outputs)
	if err != nil {
		return nil, err
	}
	rules, err := allow(opts.SecurityRules)
	if err != nil {
		return nil, err
	}
	o := &Observer{routes: routes, outputs: outputs, rules: rules, last: make(map[string]apilog.OutputHealth)}
	if opts.SlowThreshold < 0 {
		return nil, errors.New("apilog/prometheus: negative slow threshold")
	}
	o.slowThreshold = opts.SlowThreshold
	if o.slowThreshold == 0 {
		o.slowThreshold = 200 * time.Millisecond
	}
	var collectors []prometheus.Collector
	if !opts.DisableAPI {
		o.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_api_requests_total", Help: "Observed API requests after capture policy."}, []string{"route", "method", "status_class"})
		o.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_api_duration_seconds", Help: "Observed HTTP handler duration.", Buckets: prometheus.DefBuckets}, []string{"route", "method"})
		o.active = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apilog_api_active_requests", Help: "Requests currently executing, grouped by fixed HTTP method."}, []string{"method"})
		o.sizes = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_api_body_bytes", Help: "Observed body bytes; unread and upgraded traffic is not inferred.", Buckets: prometheus.ExponentialBuckets(128, 4, 9)}, []string{"route", "direction"})
		o.slow = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_api_slow_requests_total", Help: "Requests at or above the configured duration threshold."}, []string{"route"})
		o.exceptions = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_api_exceptions_total", Help: "Handler panics observed by capture; does not infer exceptions from HTTP status."}, []string{"route"})
		o.rateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_api_rate_limited_total", Help: "Responses with HTTP status 429."}, []string{"route"})
		collectors = append(collectors, o.requests, o.duration, o.active, o.sizes, o.slow, o.exceptions, o.rateLimited)
	}
	if !opts.DisableProfiling {
		o.queries = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_profile_query_count", Help: "Observed query count per instrumented request.", Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250, 1000}}, []string{"route"})
		o.sqlDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_profile_sql_duration_seconds", Help: "Cumulative observed SQL duration; overlapping queries are summed.", Buckets: prometheus.DefBuckets}, []string{"route"})
		o.duplicates = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_profile_duplicate_query_count", Help: "Repeated query fingerprints per instrumented request.", Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 1000}}, []string{"route"})
		o.nplusone = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_profile_n_plus_one_hints_total", Help: "Requests containing a possible N+1 heuristic, not confirmed N+1 defects."}, []string{"route"})
		collectors = append(collectors, o.queries, o.sqlDuration, o.duplicates, o.nplusone)
	}
	if !opts.DisableSecurity {
		o.signals = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_security_signals_total", Help: "Security hints by allowlisted rule and fixed severity."}, []string{"rule", "severity"})
		collectors = append(collectors, o.signals)
	}
	if !opts.DisableHealth {
		o.queue = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apilog_health_queue_entries", Help: "Current queued events."}, []string{"output"})
		o.queueBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apilog_health_queue_bytes", Help: "Current queued serialized bytes."}, []string{"output"})
		o.workers = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apilog_health_worker_running", Help: "Whether the output worker is running."}, []string{"output"})
		o.pipeline = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_health_events_total", Help: "Output event lifecycle counters."}, []string{"output", "outcome"})
		o.writeDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apilog_health_last_write_seconds", Help: "Duration of the latest batch write."}, []string{"output"})
		o.timing = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_health_operation_duration_seconds", Help: "Logger work measured on the request path.", Buckets: []float64{0.000001, 0.00001, 0.0001, 0.001, 0.01, 0.1}}, []string{"route", "stage"})
		o.skipped = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_health_skipped_total", Help: "Events skipped before queue admission."}, []string{"reason"})
		o.batchSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_health_batch_size", Help: "Events in a completed batch write attempt.", Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 1000}}, []string{"output"})
		o.flushDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apilog_health_flush_duration_seconds", Help: "Duration of a completed batch write attempt.", Buckets: prometheus.DefBuckets}, []string{"output"})
		o.batches = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apilog_health_batches_total", Help: "Completed batch write attempts."}, []string{"output"})
		collectors = append(collectors, o.queue, o.queueBytes, o.workers, o.pipeline, o.writeDuration, o.timing, o.skipped, o.batchSize, o.flushDuration, o.batches)
	}
	for i, c := range collectors {
		if err := reg.Register(c); err != nil {
			for _, previous := range collectors[:i] {
				reg.Unregister(previous)
			}
			return nil, err
		}
	}
	return o, nil
}
func bounded(value string, set map[string]bool) string {
	if set[value] {
		return value
	}
	return "other"
}
func method(value string) string {
	switch value {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return value
	}
	return "OTHER"
}
func class(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code < 300 && code >= 200:
		return "2xx"
	case code < 400 && code >= 300:
		return "3xx"
	case code < 500 && code >= 400:
		return "4xx"
	case code < 600 && code >= 500:
		return "5xx"
	}
	return "other"
}
func severity(value string) string {
	switch value {
	case "low", "medium", "high", "critical", "info", "warning", "notice":
		return value
	}
	return "other"
}
func (o *Observer) Observe(_ context.Context, e apilog.Event) {
	route := bounded(e.Route, o.routes)
	if o.requests != nil {
		o.requests.WithLabelValues(route, method(e.Method), class(e.Status)).Inc()
		o.duration.WithLabelValues(route, method(e.Method)).Observe(e.Duration.Seconds())
		o.sizes.WithLabelValues(route, "request").Observe(float64(e.Request.Bytes))
		o.sizes.WithLabelValues(route, "response").Observe(float64(e.Response.Bytes))
		if e.Duration >= o.slowThreshold {
			o.slow.WithLabelValues(route).Inc()
		}
		if e.Panicked {
			o.exceptions.WithLabelValues(route).Inc()
		}
		if e.Status == 429 {
			o.rateLimited.WithLabelValues(route).Inc()
		}
	}
	if o.queries != nil && e.Profile != nil && e.Profile.Instrumented {
		o.queries.WithLabelValues(route).Observe(float64(e.Profile.QueryCount))
		o.sqlDuration.WithLabelValues(route).Observe(e.Profile.SQLDuration.Seconds())
		o.duplicates.WithLabelValues(route).Observe(float64(e.Profile.DuplicateQueries))
		for _, d := range e.Profile.Diagnostics {
			if strings.HasPrefix(d, "Possible N+1") {
				o.nplusone.WithLabelValues(route).Inc()
				break
			}
		}
	}
	if o.signals != nil {
		for _, s := range e.Security {
			o.signals.WithLabelValues(bounded(s.RuleID, o.rules), severity(s.Severity)).Inc()
		}
	}
}
func delta(current, old uint64) float64 {
	if current >= old {
		return float64(current - old)
	}
	return 0
}
func (o *Observer) Pipeline(name string, h apilog.OutputHealth) {
	if o.pipeline == nil || !o.outputs[name] {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	old := o.last[name]
	o.last[name] = apilog.OutputHealth{Accepted: max(h.Accepted, old.Accepted), Delivered: max(h.Delivered, old.Delivered), Dropped: max(h.Dropped, old.Dropped), Failed: max(h.Failed, old.Failed), BatchCount: max(h.BatchCount, old.BatchCount)}
	for key, n := range map[string]float64{"accepted": delta(h.Accepted, old.Accepted), "delivered": delta(h.Delivered, old.Delivered), "dropped": delta(h.Dropped, old.Dropped), "failed": delta(h.Failed, old.Failed)} {
		o.pipeline.WithLabelValues(name, key).Add(n)
	}
	o.queue.WithLabelValues(name).Set(float64(h.Queued))
	o.queueBytes.WithLabelValues(name).Set(float64(h.QueuedBytes))
	running := 0.0
	if h.WorkerRunning {
		running = 1
	}
	o.workers.WithLabelValues(name).Set(running)
	o.writeDuration.WithLabelValues(name).Set(h.LastWriteDuration.Seconds())
	if h.BatchCount > old.BatchCount {
		o.batches.WithLabelValues(name).Add(float64(h.BatchCount - old.BatchCount))
		o.batchSize.WithLabelValues(name).Observe(float64(h.LastBatchSize))
		o.flushDuration.WithLabelValues(name).Observe(h.LastWriteDuration.Seconds())
	}
}

func (o *Observer) RequestStarted(_ context.Context, e apilog.Event) {
	if o.active != nil {
		o.active.WithLabelValues(method(e.Method)).Inc()
	}
}
func (o *Observer) RequestFinished(_ context.Context, e apilog.Event) {
	if o.active != nil {
		o.active.WithLabelValues(method(e.Method)).Dec()
	}
}
func (o *Observer) ObserveTiming(route string, t apilog.Timing) {
	if o.timing == nil {
		return
	}
	for stage, d := range map[string]time.Duration{"capture": t.Capture, "mask": t.Mask, "serialize": t.Serialize, "enqueue": t.Enqueue, "total": t.Total} {
		o.timing.WithLabelValues(bounded(route, o.routes), stage).Observe(d.Seconds())
	}
}
func (o *Observer) Skipped(reason string) {
	if o.skipped == nil {
		return
	}
	switch reason {
	case "policy", "method", "route", "path", "status", "closed", "invalid", "transform", "sampling":
	default:
		reason = "other"
	}
	o.skipped.WithLabelValues(reason).Inc()
}
