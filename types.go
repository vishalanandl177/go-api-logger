// Package apilog provides framework-independent API logging and profiling.
package apilog

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const SchemaVersion = 1

var ErrNotFound = errors.New("apilog: event not found")
var ErrClosed = errors.New("apilog: logger closed")

type Body struct {
	Encoding    string          `json:"encoding,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	State       string          `json:"state"`
	Bytes       int64           `json:"bytes"`
	ContentType string          `json:"content_type,omitempty"`
}

// Event owns all its fields. Sinks may retain their batch; events never hold a
// request, response writer, framework context, or live database object.
type Event struct {
	Version         int                 `json:"version"`
	ID              string              `json:"id"`
	Time            time.Time           `json:"time"`
	Duration        time.Duration       `json:"duration_ns"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	Path            string              `json:"path"`
	Route           string              `json:"route"`
	Name            string              `json:"name,omitempty"`
	Group           string              `json:"group,omitempty"`
	Handler         string              `json:"handler,omitempty"`
	Protocol        string              `json:"protocol,omitempty"`
	ClientIP        string              `json:"client_ip,omitempty"`
	Status          int                 `json:"status"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	Request         Body                `json:"request"`
	Response        Body                `json:"response"`
	RequestID       string              `json:"request_id,omitempty"`
	TraceID         string              `json:"trace_id,omitempty"`
	Context         map[string]string   `json:"context,omitempty"`
	Profile         *Profile            `json:"profile,omitempty"`
	Panicked        bool                `json:"panicked,omitempty"`
	Security        []SecuritySignal    `json:"security,omitempty"`
	ExportDisabled  bool                `json:"export_disabled,omitempty"`
	CaptureDuration time.Duration       `json:"-"`
}

type Profile struct {
	Instrumented     bool                     `json:"instrumented"`
	Incomplete       bool                     `json:"incomplete"`
	QueryCount       int                      `json:"query_count"`
	DuplicateQueries int                      `json:"duplicate_queries"`
	SQLDuration      time.Duration            `json:"sql_duration_ns"`
	SQLWallTime      time.Duration            `json:"sql_wall_time_ns"`
	HandlerDuration  time.Duration            `json:"handler_duration_ns"`
	LoggerOverhead   time.Duration            `json:"logger_overhead_ns"`
	Stages           map[string]time.Duration `json:"stages,omitempty"`
	Diagnostics      []string                 `json:"diagnostics,omitempty"`
}

type SecuritySignal struct {
	RuleID   string `json:"rule_id"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Score    int    `json:"score"`
}

type Sink interface {
	WriteBatch(context.Context, []Event) error
}
type SinkFunc func(context.Context, []Event) error

func (f SinkFunc) WriteBatch(ctx context.Context, events []Event) error { return f(ctx, events) }

type Output struct {
	Name string
	// Kind is "storage" or "export" and enables independent policy gates.
	Kind string
	Sink Sink
}

type Filter struct {
	Search         string
	Methods        []string
	StatusCodes    []int
	After, Before  time.Time
	Slow           *bool
	SlowThreshold  time.Duration
	SQLMin, SQLMax *int
	Profiled       *bool
	IDs            []string
	Order          string
	Descending     bool
	Page, Limit    int
}
type Page struct {
	Events      []Event `json:"events"`
	Total       int64   `json:"total"`
	Page, Limit int
}
type DayStat struct {
	Day           string  `json:"day"`
	Count         int64   `json:"count"`
	AverageSQL    float64 `json:"average_sql"`
	ProfiledCount int64   `json:"profiled_count"`
}
type StatusStat struct {
	Status int   `json:"status"`
	Count  int64 `json:"count"`
}
type Analytics struct {
	Days     []DayStat    `json:"days"`
	Statuses []StatusStat `json:"statuses"`
}
type PruneOptions struct {
	Before    time.Time
	BatchSize int
	DryRun    bool
}
type PruneResult struct{ Matched, Deleted int64 }

type Store interface {
	Sink
	Migrate(context.Context) error
	Check(context.Context) error
	List(context.Context, Filter) (Page, error)
	Get(context.Context, string) (Event, error)
	Aggregate(context.Context, Filter) (Analytics, error)
	Delete(context.Context, []string) (int64, error)
	Prune(context.Context, PruneOptions) (PruneResult, error)
}

// Observer callbacks are synchronous and must be bounded, concurrency-safe,
// and free of network I/O. Events passed to Observe are already sanitized.
type Observer interface {
	Observe(context.Context, Event)
	Pipeline(string, OutputHealth)
}

type RequestObserver interface {
	RequestStarted(context.Context, Event)
	RequestFinished(context.Context, Event)
}
type Timing struct{ Capture, Mask, Serialize, Enqueue, Total time.Duration }
type TimingObserver interface {
	ObserveTiming(route string, timing Timing)
}
type SkipObserver interface{ Skipped(reason string) }
type OutputHealth struct {
	Accepted, Delivered, Dropped, Failed uint64
	Queued, QueuedBytes                  int
	InFlight                             int
	WorkerRunning                        bool
	LastWriteDuration                    time.Duration
	LastBatchSize                        int
	BatchCount                           uint64
}
type Health struct {
	Closed  bool
	Skipped uint64
	Outputs map[string]OutputHealth
}
