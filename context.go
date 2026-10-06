package apilog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type contextKey struct{}
type suppressKey struct{}
type interval struct{ start, end time.Time }
type Exchange struct {
	mu                            sync.Mutex
	logger                        *Logger
	ctx                           context.Context
	event                         Event
	start                         time.Time
	profile                       *Profile
	queries                       map[string]int
	intervals                     []interval
	active                        int
	finished                      bool
	suppressed                    bool
	requestSample, responseSample []byte
	captureDuration               time.Duration
}

// Begin creates an exchange and shared request-local context. Adapters must call
// Finish exactly once after response rendering, including their panic path.
func (l *Logger) Begin(ctx context.Context, e Event) (context.Context, *Exchange) {
	x := &Exchange{logger: l, event: e, start: time.Now(), queries: make(map[string]int)}
	if l.config.Profile.Enabled && rand.Float64() < l.config.Profile.SampleRate {
		x.profile = &Profile{Stages: make(map[string]time.Duration)}
	}
	ctx = context.WithValue(ctx, contextKey{}, x)
	x.ctx = ctx
	if observer, ok := l.config.Observer.(RequestObserver); ok {
		safeEvent := Event{Method: cleanText(e.Method, 32)}
		func() { defer func() { _ = recover() }(); observer.RequestStarted(ctx, safeEvent) }()
	}
	return ctx, x
}
func exchange(ctx context.Context) *Exchange { x, _ := ctx.Value(contextKey{}).(*Exchange); return x }

// SkipRequest excludes the current exchange from payload capture and outputs.
// Embedded administrative handlers use it to avoid recording themselves.
func SkipRequest(ctx context.Context) {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.suppressed = true
	}
}

func requestSkipped(ctx context.Context) bool {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.suppressed
	}
	return false
}
func SetRoute(ctx context.Context, route, name, group string) {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		if !x.finished {
			x.event.Route = route
			x.event.Name = name
			x.event.Group = group
		}
	}
}

// SetRoutePattern supplies a generic router fallback without overwriting native
// framework route names or groups already attached to the shared context.
func SetRoutePattern(ctx context.Context, route string) {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		if !x.finished && x.event.Route == "" {
			x.event.Route = route
		}
	}
}
func SetHandler(ctx context.Context, name string) {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		if !x.finished {
			x.event.Handler = name
		}
	}
}
func SetContext(ctx context.Context, values map[string]string) {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.finished {
			return
		}
		if x.event.Context == nil {
			x.event.Context = map[string]string{}
		}
		for k, v := range values {
			if len(x.event.Context) >= 32 {
				break
			}
			x.event.Context[cleanText(k, 64)] = cleanText(v, 256)
		}
	}
}
func RequestID(ctx context.Context) string {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.event.RequestID
	}
	return ""
}
func TraceID(ctx context.Context) string {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.event.TraceID
	}
	return ""
}

// SetTraceID associates an existing tracer's canonical 128-bit trace ID.
func SetTraceID(ctx context.Context, id string) {
	if len(id) != 32 || id == strings.Repeat("0", 32) {
		return
	}
	if _, err := hex.DecodeString(id); err != nil {
		return
	}
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		if !x.finished {
			x.event.TraceID = strings.ToLower(id)
		}
	}
}

// InspectSamples accepts bounded temporary samples only when the corresponding
// security inspection option is enabled. Samples are never part of an Event.
func (x *Exchange) InspectSamples(request, response []byte) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.finished || x.logger.security == nil {
		return
	}
	c := x.logger.config.Security
	if c.InspectRequest {
		x.requestSample = append([]byte(nil), request[:min(len(request), c.SampleBytes)]...)
	}
	if c.InspectResponse {
		x.responseSample = append([]byte(nil), response[:min(len(response), c.SampleBytes)]...)
	}
}

func (x *Exchange) AddOverhead(d time.Duration) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.finished {
		x.captureDuration += d
	}
	if x.profile != nil && !x.finished {
		x.profile.LoggerOverhead += d
	}
}
func ProfileEnabled(ctx context.Context) bool {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		return x.profile != nil && !x.finished
	}
	return false
}
func MarkInstrumented(ctx context.Context) {
	if x := exchange(ctx); x != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.profile != nil && !x.finished {
			x.profile.Instrumented = true
		}
	}
}
func SuppressProfiling(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressKey{}, true)
}
func ProfilingSuppressed(ctx context.Context) bool { b, _ := ctx.Value(suppressKey{}).(bool); return b }

// CaptureAllowed is checked when the body is actually read, after router
// enrichers have had an opportunity to install route metadata.
func (x *Exchange) CaptureAllowed(request bool) bool {
	x.mu.Lock()
	e := x.event
	e.Context = maps.Clone(x.event.Context)
	e.RequestHeaders = copyHeaders(x.event.RequestHeaders)
	e.ResponseHeaders = copyHeaders(x.event.ResponseHeaders)
	skipped := x.suppressed
	x.mu.Unlock()
	if skipped {
		return false
	}
	d := x.logger.decision(e, false)
	return !d.Skip && !(request && d.StripRequest) && !(!request && d.StripResponse)
}
func (x *Exchange) Finish(e Event) {
	x.mu.Lock()
	if x.finished {
		x.mu.Unlock()
		return
	}
	x.finished = true
	e.ID = x.event.ID
	e.Time = x.event.Time
	e.Method = x.event.Method
	e.URL = x.event.URL
	e.Path = x.event.Path
	e.ClientIP = x.event.ClientIP
	e.Protocol = x.event.Protocol
	if x.event.Route != "" {
		e.Route = x.event.Route
	}
	e.Name = x.event.Name
	e.Group = x.event.Group
	e.Handler = x.event.Handler
	e.RequestID = x.event.RequestID
	e.TraceID = x.event.TraceID
	e.Context = x.event.Context
	if e.RequestHeaders == nil {
		e.RequestHeaders = x.event.RequestHeaders
	}
	end := time.Now()
	e.Duration = end.Sub(x.start)
	e.CaptureDuration = x.captureDuration
	if x.profile != nil {
		x.profile.HandlerDuration = e.Duration
		x.profile.Incomplete = x.profile.Incomplete || x.active > 0
		x.profile.SQLWallTime = unionDuration(x.intervals, x.start, end)
		if x.profile.Instrumented {
			if x.profile.QueryCount >= 10 && x.profile.DuplicateQueries > 0 {
				x.profile.Diagnostics = append(x.profile.Diagnostics, "Possible N+1: repeated query shapes; inspect application relationships")
			}
			if e.Duration > 0 && x.profile.SQLWallTime > e.Duration*7/10 && x.profile.QueryCount < 5 {
				x.profile.Diagnostics = append(x.profile.Diagnostics, "A few database operations dominate observed elapsed time")
			}
			if e.Duration > x.logger.config.SlowThreshold && x.profile.SQLWallTime < e.Duration/5 {
				x.profile.Diagnostics = append(x.profile.Diagnostics, "Most elapsed time is outside observed database operations")
			}
		}
		e.Profile = x.profile
	}
	x.mu.Unlock()
	if x.logger.security != nil {
		securityEvent := e
		if x.requestSample != nil {
			securityEvent.Request.Data = x.requestSample
		}
		if x.responseSample != nil {
			securityEvent.Response.Data = x.responseSample
		}
		e.Security = x.logger.security.inspect(securityEvent)
		x.requestSample = nil
		x.responseSample = nil
	}
	defer func() {
		if observer, ok := x.logger.config.Observer.(RequestObserver); ok {
			func() {
				defer func() { _ = recover() }()
				observer.RequestFinished(x.ctx, Event{Method: cleanText(e.Method, 32)})
			}()
		}
	}()
	x.logger.Emit(x.ctx, e)
}

var sqlLiterals = regexp.MustCompile(`(?s)'(?:''|[^'])*'|"(?:""|[^"])*"|\b[0-9]+(?:\.[0-9]+)?\b|/\*.*?\*/|--[^\r\n]*`)

func fingerprint(statement string) string {
	// Only bounded, normalized hashes are retained, never SQL or bound arguments.
	if len(statement) > 64<<10 {
		return "oversized"
	}
	normal := strings.ToLower(strings.Join(strings.Fields(sqlLiterals.ReplaceAllString(statement, "?")), " "))
	h := sha256.Sum256([]byte(normal))
	return hex.EncodeToString(h[:])
}
func StartQuery(ctx context.Context, source, statement string) func(error) {
	return StartQueryAt(ctx, source, statement, time.Now())
}
func StartQueryAt(ctx context.Context, source, statement string, start time.Time) func(error) {
	x := exchange(ctx)
	if x == nil || ProfilingSuppressed(ctx) {
		return func(error) {}
	}
	x.mu.Lock()
	if x.finished || x.profile == nil {
		x.mu.Unlock()
		return func(error) {}
	}
	x.profile.Instrumented = true
	x.profile.QueryCount++
	x.active++
	key := cleanText(source, 64) + ":" + fingerprint(statement)
	if len(x.queries) < x.logger.config.Profile.MaxQueries || x.queries[key] > 0 {
		if x.queries[key] > 0 {
			x.profile.DuplicateQueries++
		}
		x.queries[key]++
	} else {
		x.profile.Incomplete = true
	}
	x.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			end := time.Now()
			x.mu.Lock()
			defer x.mu.Unlock()
			if x.finished {
				return
			}
			x.active--
			x.profile.SQLDuration += max(0, end.Sub(start))
			if len(x.intervals) < x.logger.config.Profile.MaxQueries {
				x.intervals = append(x.intervals, interval{start, end})
			} else {
				x.profile.Incomplete = true
			}
		})
	}
}
func RecordQuery(ctx context.Context, source, statement string, start time.Time, err error) {
	StartQueryAt(ctx, source, statement, start)(err)
}
func StartStage(ctx context.Context, name string) func() {
	start := time.Now()
	x := exchange(ctx)
	var once sync.Once
	if x == nil {
		return func() {}
	}
	x.mu.Lock()
	if x.finished || x.profile == nil {
		x.mu.Unlock()
		return func() {}
	}
	x.active++
	x.mu.Unlock()
	return func() {
		once.Do(func() {
			x.mu.Lock()
			defer x.mu.Unlock()
			if x.finished {
				return
			}
			x.active--
			name = cleanText(name, 64)
			if _, ok := x.profile.Stages[name]; ok || len(x.profile.Stages) < 32 {
				x.profile.Stages[name] += time.Since(start)
			} else {
				x.profile.Incomplete = true
			}
		})
	}
}
func unionDuration(intervals []interval, start, end time.Time) time.Duration {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	var total time.Duration
	a, b := start, start
	for _, in := range intervals {
		if in.start.Before(start) {
			in.start = start
		}
		if in.end.After(end) {
			in.end = end
		}
		if !in.end.After(in.start) {
			continue
		}
		if in.start.After(b) {
			total += b.Sub(a)
			a, b = in.start, in.end
		} else if in.end.After(b) {
			b = in.end
		}
	}
	return total + b.Sub(a)
}
