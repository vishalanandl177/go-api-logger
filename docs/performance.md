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

The microbenchmarks isolate much smaller operations and use a small payload. They do not represent end-to-end HTTP service capacity or prove delivery under load. Run the tests and measurement workload with the actual payload sizes, SQL drivers, queue settings, and database infrastructure used by the application.

## Recorded local results

Recorded on 6 October 2026, Windows amd64, Intel Core i9-14900KS, 32 logical CPUs, 68,481,736,704 bytes of physical RAM, and `GOMAXPROCS=2`. The HTTP table uses Go 1.27.1, 5000 measured requests per scenario, eight clients and 200 warmup requests. The application pool had one connection. The machine was also running development tools, so results are not an isolated laboratory comparison.

| Configuration | Requests/s | p50, ms | p95, ms | p99, ms | Process allocated bytes/request | Allocations/request |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 24,224 | unresolved* | 1.085 | 1.525 | 15,308 | 141 |
| Metadata + discard sink | 14,440 | 0.500 | 1.539 | 2.426 | 28,085 | 236 |
| Bodies + discard sink | 7,661 | 0.999 | 2.508 | 3.502 | 88,738 | 318 |
| Bodies + SQLite log store | 4,791 | 1.499 | 4.003 | 6.498 | 116,906 | 372 |
| Bodies + instrumented SQL | 6,540 | 1.015 | 3.095 | 4.659 | 89,730 | 337 |
| Bodies + timed-out sink | 5,308 | 1.034 | 3.681 | 5.339 | 88,427 | 319 |

*The host produced zero-duration samples at the baseline median. Those samples are retained as `0` in the raw JSON but do not mean the requests took no time. The local per-request clock measurements cannot resolve that median reliably. A Go 1.26.2 run showed the same limitation for baseline and metadata. Aggregate Go benchmark timings remain useful because they measure many iterations over a longer interval.

Every HTTP request succeeded. Metadata, bodies, SQLite, and profiling each delivered all 5000 measured events in this run. SQLite peak observed queue usage was 788 events and 4,589,174 bytes, below the configured 1024 events and 16 MiB. A faster producer, slower disk or larger payload can saturate those limits and cause intentional drops.

Raw evidence: [Go 1.27.1 HTTP results](performance-windows-go1.27.1.json), [Go 1.26.2 HTTP results](performance-windows-go1.26.2.json), and [Go 1.26.2 microbenchmark output](performance-microbench-windows-go1.26.2.txt).

The three-run Go 1.26.2 microbenchmark medians were:

| Operation | Median ns/op | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Middleware baseline | 1,944 | 6,104 | 18 |
| Middleware metadata | 9,786 | 13,833 | 66 |
| Middleware body | 12,558 | 16,258 | 87 |
| Emit body to discard worker | 19,881 | 12,793 | 124 |
| Emit metadata to discard worker | 20,208 | about 12,487 | 121 |

These are separately shaped benchmarks. The small Emit fixture does not show a reliable body-versus-metadata speed advantage; do not infer one from the names.

## Sink outage and memory limits

The outage scenario sets capacity to 64 events, the serialized-event byte budget to 1 MiB, batch size to 16 and write timeout to 100 ms. Its sink waits for its context deadline and fails every batch. It does not use an uncooperative sink that ignores cancellation.

For 5000 measured requests, 208 events were accepted and subsequently failed, 4792 were dropped at admission, and none were delivered. All 5000 HTTP responses succeeded. Peak observed occupancy was exactly 64 events and 372,912 serialized bytes. Process heap after GC changed from 642,768 bytes before the workload to 764,944 bytes after draining. This is evidence for this bounded workload and queue policy, not proof that process RSS is capped at the queue byte budget. HTTP clients, active requests, SQL pools, event preparation, GC and driver buffers consume memory outside the queue.

Monitor queue occupancy, drops, failures, worker state and storage write time in deployment. Tune body limits and selective capture before increasing queue capacity. Dropped or failed events are not retried or durably spooled. An API log library configured for best-effort delivery cannot substitute for a durable audit pipeline.
