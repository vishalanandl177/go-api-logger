# Performance measurements

Performance depends on captured payload size, redaction work, SQL instrumentation, sink speed, CPU, and deployment concurrency. The library keeps sink I/O off the request goroutine and bounds queued event count and bytes; capture, redaction, event copying, and local observers still consume request CPU and allocations. These measurements describe one development machine and workload, not a latency guarantee.

## Reproduce

The `examples/benchmark` command runs the same localhost HTTP workload under six configurations: baseline, metadata logging, JSON body logging, body logging persisted to SQLite, body logging with SQL profiling, and a timed-out sink. Every request decodes a 1062-byte JSON body, executes `SELECT length(?)` on a single-connection in-memory application SQLite pool, and returns 4109 bytes of JSON. Only the profiling scenario wraps that pool's connector. SQLite log persistence uses a separate temporary file and pool.

```sh
go work init . ./storage ./integrations ./cmd ./examples
cd examples
GOMAXPROCS=2 go run ./benchmark -requests 5000 -concurrency 8 -warmup 200 > performance.json
```

PowerShell:

```powershell
$env:GOMAXPROCS = '2'
go run ./benchmark -requests 5000 -concurrency 8 -warmup 200 | Set-Content -Encoding utf8 performance.json
```

Warmup requests and their log drain finish before measurement. Percentiles cover measured HTTP round trips, including application work and local network/client costs. Throughput excludes the final sink drain. Allocation deltas cover the whole process, including client, server, database, logger and workers. They are not logger-only allocations. Queue usage is sampled every 2 ms; the harness fails if an observed bound is exceeded or an HTTP request fails. Temporary database files are removed after the run.

For Go allocation microbenchmarks, run from the repository root:

```sh
GOMAXPROCS=2 go test -run '^$' -bench 'BenchmarkEmit|BenchmarkMiddleware' -benchmem -benchtime=1s -count=3 . ./httpmw
```

The middleware microbenchmark serves a GET request with no request body and an 11-byte JSON response. Its metadata and body configurations use an actual discard sink, so logging includes sanitization, event copying, queue admission and an asynchronous worker. The baseline has no logger. `BenchmarkEmit` uses a separate small fixture with a nested JSON request, masked headers/query parameters and a JSON response; both configurations also use a discard sink. Each benchmark drains after timing and reports delivered and dropped events per operation. They do not represent end-to-end HTTP service capacity. Run the tests and measurement workload with the actual payload sizes, SQL drivers, queue settings, and database infrastructure used by the application.

## Recorded local results

Recorded on 6 October 2026, Windows amd64, Intel Core i9-14900KS, 32 logical CPUs, 68,481,736,704 bytes of physical RAM, and `GOMAXPROCS=2`. The HTTP table uses the run recorded at 15:03:55 UTC with Go 1.27.1, 5000 measured requests per scenario, eight clients and 200 warmup requests. The application pool had one connection. The machine was also running development tools, so results are not an isolated laboratory comparison.

| Configuration | Requests/s | p50, ms | p95, ms | p99, ms | Process allocated bytes/request | Allocations/request |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 21,259 | 0.499 | 1.018 | 1.524 | 15,301 | 141 |
| Metadata + discard sink | 12,690 | 0.500 | 1.541 | 2.505 | 31,325 | 278 |
| Bodies + discard sink | 7,121 | 1.000 | 3.000 | 4.375 | 92,010 | 360 |
| Bodies + SQLite log store | 4,661 | 1.499 | 4.002 | 6.116 | 122,505 | 419 |
| Bodies + instrumented SQL | 5,415 | 1.018 | 3.761 | 5.500 | 93,075 | 379 |
| Bodies + timed-out sink | 5,507 | 1.033 | 3.504 | 5.139 | 91,669 | 361 |

Earlier runs on this host produced zero-duration samples at the baseline median because the per-request clock measurements could not resolve it reliably. This run resolved all reported percentiles, but the approximately 0.5 ms steps at the low end still limit fine-grained comparisons. Aggregate Go benchmark timings measure many iterations over a longer interval.

Every HTTP request succeeded. Metadata, bodies, SQLite, and profiling each delivered all 5000 measured events in this run. SQLite peak observed queue usage was 453 events and 2,638,114 bytes, below the configured 1024 events and 16 MiB. A faster producer, slower disk or larger payload can saturate those limits and cause intentional drops.

Raw evidence: [current Go 1.27.1 HTTP results](performance-windows-go1.27.1.json) and [current Go 1.26.2 microbenchmark output](performance-microbench-windows-go1.26.2.txt). The [earlier Go 1.26.2 HTTP snapshot](performance-windows-go1.26.2.json) is retained for provenance; it predates subsequent implementation changes and is not a controlled comparison between Go versions.

The three-run Go 1.26.2 microbenchmark medians were:

| Operation | Median ns/op | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Middleware baseline | 1,924 | 6,104 | 18 |
| Middleware metadata to discard worker | 20,208 | 16,882 | 96 |
| Middleware body to discard worker | 27,191 | 19,371 | 119 |
| Emit body to discard worker | 19,746 | 12,937 | 126 |
| Emit metadata to discard worker | 14,522 | 6,862 | 58 |

All logging microbenchmark runs reported `1.000 delivered/op` and `0 dropped/op`. Middleware body mode captures the response; metadata mode omits body capture. The middleware and Emit fixtures perform different work and should be compared only within their own groups. Earlier middleware numbers without an output sink did not measure this logging path and have been replaced.

## Sink outage and memory limits

The outage scenario sets capacity to 64 events, the serialized-event byte budget to 1 MiB, batch size to 16 and write timeout to 100 ms. Its sink waits for its context deadline and fails every batch. It does not use an uncooperative sink that ignores cancellation.

For 5000 measured requests, 204 events were accepted and subsequently failed, 4796 were dropped at admission, and none were delivered. All 5000 HTTP responses succeeded. Peak observed occupancy was exactly 64 events and 372,905 serialized bytes. Process heap after GC changed from 643,760 bytes before the workload to 689,640 bytes after draining. This is evidence for this bounded workload and queue policy, not proof that process RSS is capped at the queue byte budget. HTTP clients, active requests, SQL pools, event preparation, GC and driver buffers consume memory outside the queue.

Monitor queue occupancy, drops, failures, worker state and storage write time in deployment. Tune body limits and selective capture before increasing queue capacity. Dropped or failed events are not retried or durably spooled. An API log library configured for best-effort delivery cannot substitute for a durable audit pipeline.
