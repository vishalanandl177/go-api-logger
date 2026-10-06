# Go API Logger

[![CI](https://github.com/vishalanandl177/go-api-logger/actions/workflows/ci.yml/badge.svg)](https://github.com/vishalanandl177/go-api-logger/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vishalanandl177/go-api-logger.svg)](https://pkg.go.dev/github.com/vishalanandl177/go-api-logger)

Inspect API requests across Go servers with masked request/response logging, SQL storage, an embedded dashboard, request profiling, and optional observability integrations.

Inspired by [DRF-API-Logger](https://github.com/vishalanandl177/DRF-API-Logger). The core uses the Go standard library and does not depend on a framework, SQL driver, or telemetry SDK. Supports Go 1.26 and 1.27.

**Development status:** implementation and release validation are in progress. See the [parity matrix](docs/parity.md) and CI results for verified coverage. Stable module installation instructions will be finalized with the first release.

## What is included

- Standard `net/http` capture with Gin, chi, and Echo v4/v5 route integration.
- Bounded JSON request/response capture, recursive secret masking, endpoint policies, and request/trace correlation.
- Independent bounded background queues for SQL, JSON, `slog`, and custom subscribers.
- PostgreSQL, MySQL, and SQLite storage with explicit migrations and retention.
- Embedded, application-authorized search, filters, payload inspection, CSV export, deletion, and lazy analytics charts.
- Optional `database/sql`, pgx, and GORM profiling with request-local attribution.
- Optional Prometheus metrics, existing OpenTelemetry span enrichment, Sentry context, and 16 detect-only security rules.
- Operational health/doctor checks, race and fuzz tests, and reproducible benchmarks.

Native Fiber/fasthttp adapters are follow-up work. They are not included in the compatibility claim for this release.

## Quick start

The following complete application logs sanitized events as JSON lines. Applications own server lifetime and output resources.

```go
package main

import (
    "context"
    "errors"
    "log"
    "net/http"
    "os"
    "os/signal"
    "time"

    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
)

func main() {
    cfg := apilog.DefaultConfig()
    cfg.Outputs = []apilog.Output{{
        Name: "stdout", Kind: "export",
        Sink: &apilog.JSONSink{Writer: os.Stdout},
    }}
    logger, err := apilog.New(cfg)
    if err != nil { log.Fatal(err) }

    mux := http.NewServeMux()
    mux.HandleFunc("POST /hello", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{"message":"hello"}`))
    })
    server := &http.Server{
        Addr: "127.0.0.1:8080", Handler: httpmw.Middleware(logger)(mux),
        ReadHeaderTimeout: 5 * time.Second,
    }
    stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()
    go func() {
        <-stop.Done()
        ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
        defer done()
        _ = server.Shutdown(ctx)
    }()
    if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
        log.Print(err)
    }
    ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
    defer done()
    if err := logger.Shutdown(ctx); err != nil { log.Print(err) }
}
```

The logger observes request bodies only as your handler reads them. The example above does not read a request body, so its capture state is `unread`. It never drains a body simply to log it.

## Choose only the packages you need

| Module | Packages |
| --- | --- |
| `github.com/vishalanandl177/go-api-logger` | `apilog`, `httpmw`, `dashboard` |
| `github.com/vishalanandl177/go-api-logger/storage` | PostgreSQL, MySQL, SQLite stores |
| `github.com/vishalanandl177/go-api-logger/integrations` | Frameworks, SQL profiling, Prometheus, OpenTelemetry, Sentry |
| `github.com/vishalanandl177/go-api-logger/cmd` | `apilog` operations command |
| `github.com/vishalanandl177/go-api-logger/examples` | Runnable application and benchmark harness |

Integration dependencies are isolated from the standard-library core. The initial integrations module groups the supported SDKs together; applications import only the adapters they use.

## Configuration defaults

Start with `apilog.DefaultConfig()` and change the fields you need.

| Setting | Default |
| --- | --- |
| Request / response body limit | 32 KiB / 64 KiB |
| Body formats | JSON and `application/*+json` |
| Queue per output | 1,024 events or 16 MiB serialized bytes, including in-flight batches |
| Batch size / flush interval | 50 events / 10 seconds |
| Output write deadline | 5 seconds |
| Queue overflow | Drop new events and increment health counters |
| Retries / durable spool | Disabled |
| Slow request threshold | 200 milliseconds |
| Profiling, correlation, security inspection | Disabled until configured |
| Dashboard | Created and mounted explicitly with application authorization |
| Schema migrations | Explicit API or CLI invocation |

Metadata, profiling collections, and security state are bounded too. Invalid or partially captured JSON is omitted, never saved as a raw fallback. Direct peers are the client address default; trusted proxy CIDRs must be configured before forwarded headers are honored.

## Guides

- [Configuration, policies, and custom outputs](docs/configuration.md)
- [Database storage and migrations](docs/storage.md)
- [Embedded dashboard and authorization](docs/dashboard.md)
- [Frameworks and observability integrations](docs/integrations.md)
- [SQL profiling and timing semantics](docs/profiling.md)
- [Operations, retention, and diagnostics](docs/operations.md)
- [Security signals and privacy](docs/security.md)
- [Feature parity and compatibility](docs/parity.md)
- [Performance methodology and results](docs/performance.md)
- [Contributing and local verification](CONTRIBUTING.md)

## Delivery guarantees

This library provides best-effort operational logging. A full queue, output failure, shutdown deadline, or process crash can lose events. Watch `logger.Health()` and `logger.Diagnose()` or the Prometheus integration. `Flush` waits for accepted events to be settled; inspect health counters to distinguish successful delivery from failure.

Custom sinks must honor context cancellation. Go cannot forcibly stop a callback that ignores its context. Each output has one worker, so such a callback cannot create an unbounded goroutine backlog. Observer and policy hooks are synchronous, must be bounded, and must not perform network I/O.

## License

[Apache License 2.0](LICENSE). See [NOTICE](NOTICE).
