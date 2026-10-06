// Command benchmark measures local HTTP workloads and bounded sink failure behavior.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
	apisql "github.com/vishalanandl177/go-api-logger/integrations/sql"
	"github.com/vishalanandl177/go-api-logger/storage"
	"modernc.org/sqlite"
)

type config struct {
	Requests, Concurrency, Warmup int
	Directory                     string
}
type result struct {
	Scenario                                                      string `json:"scenario"`
	Requests, Concurrency                                         int
	ElapsedSeconds, RequestsPerSecond                             float64
	P50Microseconds, P95Microseconds, P99Microseconds             float64
	ProcessAllocatedBytesPerRequest, ProcessAllocationsPerRequest float64
	HeapBeforeBytes, HeapAfterGCBytes                             uint64
	RequestFailures                                               int64
	Accepted, Delivered, Dropped, Failed                          uint64
	ObservedPeakEvents, ObservedPeakBytes                         int
	QueueCapacity, QueueByteLimit                                 int
	ObservedWithinQueueLimits                                     bool
}
type report struct {
	RecordedAt                  time.Time `json:"recorded_at"`
	GoVersion, GOOS, GOARCH     string
	LogicalCPUs, GOMAXPROCS     int
	WarmupRequests              int
	RequestBytes, ResponseBytes int
	Notes                       []string
	Results                     []result
}

var requestPayload = []byte(`{"name":"` + strings.Repeat("x", 1024) + `","token":"benchmark-secret"}`)
var responsePayload = []byte(`{"result":"` + strings.Repeat("y", 4096) + `"}`)

func scenario(ctx context.Context, cfg config, name string) (result, error) {
	res := result{Scenario: name, Requests: cfg.Requests, Concurrency: cfg.Concurrency, ObservedWithinQueueLimits: true}
	connector, err := sqlite.NewConnector(":memory:")
	if err != nil {
		return res, errors.New("application connector failed")
	}
	var appDB *sql.DB
	if name == "profiling" {
		appDB = apisql.OpenDB(connector)
	} else {
		appDB = sql.OpenDB(connector)
	}
	appDB.SetMaxOpenConns(1)
	defer appDB.Close()
	if appDB.PingContext(ctx) != nil {
		return res, errors.New("application database failed")
	}
	var logger *apilog.Logger
	var logDB *sql.DB
	if name != "baseline" {
		lc := apilog.DefaultConfig()
		lc.Queue = apilog.QueueConfig{Capacity: 1024, MaxBytes: 16 << 20, BatchSize: 50, FlushInterval: 100 * time.Millisecond, WriteTimeout: time.Second}
		lc.MetadataOnly = name == "metadata"
		lc.Profile.Enabled = name == "profiling"
		var sink apilog.Sink = apilog.SinkFunc(func(context.Context, []apilog.Event) error { return nil })
		if name == "sqlite" {
			var store *storage.SQLStore
			store, logDB, err = storage.OpenSQLite(ctx, filepath.Join(cfg.Directory, "benchmark-logs.db"))
			if err != nil {
				return res, err
			}
			defer logDB.Close()
			if err = store.Migrate(ctx); err != nil {
				return res, err
			}
			sink = store
		}
		if name == "outage" {
			lc.Queue = apilog.QueueConfig{Capacity: 64, MaxBytes: 1 << 20, BatchSize: 16, FlushInterval: 50 * time.Millisecond, WriteTimeout: 100 * time.Millisecond}
			sink = apilog.SinkFunc(func(ctx context.Context, _ []apilog.Event) error { <-ctx.Done(); return ctx.Err() })
		}
		lc.Outputs = []apilog.Output{{Name: "sink", Kind: "storage", Sink: sink}}
		logger, err = apilog.New(lc)
		if err != nil {
			return res, err
		}
		res.QueueCapacity = lc.Queue.Capacity
		res.QueueByteLimit = lc.Queue.MaxBytes
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = logger.Shutdown(stop)
		}()
	}
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Name, Token string }
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			w.WriteHeader(400)
			return
		}
		var length int
		if appDB.QueryRowContext(r.Context(), "SELECT length(?)", input.Name).Scan(&length) != nil || length != 1024 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responsePayload)
	})
	var handler http.Handler = base
	if logger != nil {
		handler = httpmw.Middleware(logger)(handler)
	}
	var serving sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serving.Add(1)
		defer serving.Done()
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	transport := &http.Transport{MaxIdleConns: cfg.Concurrency * 2, MaxIdleConnsPerHost: cfg.Concurrency * 2, MaxConnsPerHost: cfg.Concurrency}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	do := func() bool {
		request, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/benchmark", bytes.NewReader(requestPayload))
		if err != nil {
			return false
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		n, err := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return err == nil && response.StatusCode == 200 && n == int64(len(responsePayload))
	}
	for range cfg.Warmup {
		if !do() {
			return res, errors.New("warmup request failed")
		}
	}
	serving.Wait()
	if logger != nil {
		flush, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = logger.Flush(flush)
		cancel()
		if err != nil {
			return res, errors.New("warmup flush failed")
		}
	}
	var initial apilog.OutputHealth
	if logger != nil {
		initial = logger.Health().Outputs["sink"]
	}
	latencies := make([]time.Duration, cfg.Requests)
	var next atomic.Int64
	var failures atomic.Int64
	var peakEvents, peakBytes atomic.Int64
	var overLimit atomic.Bool
	stopMonitoring := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopMonitoring:
				return
			case <-ticker.C:
				if logger != nil {
					health := logger.Health().Outputs["sink"]
					events := health.Queued + health.InFlight
					if int64(events) > peakEvents.Load() {
						peakEvents.Store(int64(events))
					}
					if int64(health.QueuedBytes) > peakBytes.Load() {
						peakBytes.Store(int64(health.QueuedBytes))
					}
					if events > res.QueueCapacity || health.QueuedBytes > res.QueueByteLimit {
						overLimit.Store(true)
					}
				}
			}
		}
	}()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	res.HeapBeforeBytes = before.HeapAlloc
	start := time.Now()
	var wg sync.WaitGroup
	for range cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= cfg.Requests {
					return
				}
				requestStart := time.Now()
				if !do() {
					failures.Add(1)
				}
				latencies[index] = time.Since(requestStart)
			}
		}()
	}
	wg.Wait()
	serving.Wait()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	close(stopMonitoring)
	<-monitorDone
	res.ElapsedSeconds = elapsed.Seconds()
	res.RequestsPerSecond = float64(cfg.Requests) / elapsed.Seconds()
	res.RequestFailures = failures.Load()
	res.ProcessAllocatedBytesPerRequest = float64(after.TotalAlloc-before.TotalAlloc) / float64(cfg.Requests)
	res.ProcessAllocationsPerRequest = float64(after.Mallocs-before.Mallocs) / float64(cfg.Requests)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	percentile := func(p float64) float64 {
		index := min(len(latencies)-1, max(0, int(math.Ceil(p*float64(len(latencies))))-1))
		return float64(latencies[index]) / float64(time.Microsecond)
	}
	res.P50Microseconds = percentile(.5)
	res.P95Microseconds = percentile(.95)
	res.P99Microseconds = percentile(.99)
	if logger != nil {
		flush, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = logger.Flush(flush)
		cancel()
		if err != nil {
			return res, errors.New("measurement flush failed")
		}
		health := logger.Health().Outputs["sink"]
		res.Accepted = health.Accepted - initial.Accepted
		res.Delivered = health.Delivered - initial.Delivered
		res.Dropped = health.Dropped - initial.Dropped
		res.Failed = health.Failed - initial.Failed
	}
	res.ObservedPeakEvents = int(peakEvents.Load())
	res.ObservedPeakBytes = int(peakBytes.Load())
	res.ObservedWithinQueueLimits = !overLimit.Load()
	runtime.GC()
	runtime.ReadMemStats(&after)
	res.HeapAfterGCBytes = after.HeapAlloc
	return res, nil
}

func run(cfg config) (report, error) {
	r := report{RecordedAt: time.Now().UTC(), GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), WarmupRequests: cfg.Warmup, RequestBytes: len(requestPayload), ResponseBytes: len(responsePayload), Notes: []string{"Same POST JSON decode, SQLite SELECT and 4 KiB JSON response in every scenario.", "Latency measures localhost HTTP client time, including application work; throughput excludes final log draining.", "Allocation deltas cover the entire process, including HTTP client/server, SQL, logger, and workers.", "Queue peaks are sampled every 2 ms; in-flight events count against capacity and bytes.", "Outage sink waits for each 100 ms write timeout; losses are expected and counted.", "Heap figures are process heap bytes after GC, not a claim about total RSS or a logger-only bound."}}
	if cfg.Requests < 1 || cfg.Concurrency < 1 || cfg.Warmup < 0 {
		return r, errors.New("invalid workload size")
	}
	if cfg.Directory == "" {
		var err error
		cfg.Directory, err = os.MkdirTemp("", "apilog-benchmark-")
		if err != nil {
			return r, errors.New("temporary directory failed")
		}
		defer os.RemoveAll(cfg.Directory)
	}
	for _, name := range []string{"baseline", "metadata", "body", "sqlite", "profiling", "outage"} {
		result, err := scenario(context.Background(), cfg, name)
		if err != nil {
			return r, err
		}
		r.Results = append(r.Results, result)
		if result.RequestFailures != 0 || !result.ObservedWithinQueueLimits {
			return r, errors.New("workload or queue invariant failed")
		}
	}
	return r, nil
}
func main() {
	requests := flag.Int("requests", 5000, "measured requests per scenario")
	workers := flag.Int("concurrency", 8, "concurrent clients")
	warmup := flag.Int("warmup", 200, "warmup requests per scenario")
	flag.Parse()
	r, err := run(config{Requests: *requests, Concurrency: *workers, Warmup: *warmup})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encoder.Encode(r) != nil {
		os.Exit(1)
	}
}
