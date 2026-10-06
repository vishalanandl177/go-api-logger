# Go API Logger

[![CI](https://github.com/vishalanandl177/go-api-logger/actions/workflows/ci.yml/badge.svg)](https://github.com/vishalanandl177/go-api-logger/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vishalanandl177/go-api-logger.svg)](https://pkg.go.dev/github.com/vishalanandl177/go-api-logger)

Inspect API requests across Go servers with masked request/response logging, SQL storage, an embedded dashboard, request profiling, and optional observability integrations.

Inspired by [DRF-API-Logger](https://github.com/vishalanandl177/DRF-API-Logger). The core uses the Go standard library and does not depend on a framework, SQL driver, or telemetry SDK. Supports Go 1.26 and 1.27.

See the [parity matrix](docs/parity.md), [release notes](CHANGELOG.md), and CI results for the supported features and verification boundaries.

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

Install the core in an existing Go module:

```sh
go get github.com/vishalanandl177/go-api-logger@v1.0.1
```

Save this complete application as `main.go` in your module, then run `go run .`. It logs sanitized events as JSON lines and owns its server shutdown. The same code is available in [examples/quickstart/main.go](examples/quickstart/main.go).

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	apilog "github.com/vishalanandl177/go-api-logger"
	"github.com/vishalanandl177/go-api-logger/httpmw"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := apilog.DefaultConfig()
	cfg.Queue.FlushInterval = time.Second // Show the first log promptly in this demo.
	cfg.Correlation.Enabled = true
	cfg.Outputs = []apilog.Output{{
		Name: "stdout", Kind: "export",
		Sink: &apilog.JSONSink{Writer: os.Stdout},
	}}
	logger, err := apilog.New(cfg)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := logger.Shutdown(ctx); err != nil {
			log.Printf("logger shutdown: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", apilog.RequestID(r.Context()))
		var input struct {
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "provide one JSON object"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Hello, " + input.Name})
	})
	server := &http.Server{
		Addr: "127.0.0.1:8080", Handler: httpmw.Middleware(logger)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	serverError := make(chan error, 1)
	go func() { serverError <- server.ListenAndServe() }()
	log.Print("POST JSON to http://127.0.0.1:8080/hello; Ctrl+C to stop")
	select {
	case <-stop.Done():
	case err = <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	// Drain active handlers even when an accept/listener error stopped serving.
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := server.Shutdown(ctx)
	done()
	if shutdownErr != nil {
		_ = server.Close()
	}
	return errors.Join(err, shutdownErr)
}
```

From another terminal, send a fictional credential:

```sh
curl -H 'Content-Type: application/json' -d '{"name":"Ada","token":"example-secret"}' http://127.0.0.1:8080/hello
```

In PowerShell, use:

```powershell
Invoke-RestMethod 'http://127.0.0.1:8080/hello' -Method Post -ContentType 'application/json' -Body '{"name":"Ada","token":"example-secret"}'
```

The API returns `{"message":"Hello, Ada"}`. Within roughly one second, stdout receives an event with `request.data.token` set to `***FILTERED***`, status 200, and route `POST /hello`. The example enables correlation and shortens the flush interval for visibility. It reads the request body as part of the handler; a handler that does not read its body produces an `unread` capture state.

For the full setup workflow, see [getting started](docs/getting-started.md). For example JSON records, units and body states, see [what logs look like](docs/log-format.md).

## Use your existing HTTP server

The common boundary is `http.Handler`. Keep your router and server; wrap their handler once:

```go
server.Handler = httpmw.Middleware(logger)(existingRouter)
```

This fragment assumes `logger` was created at startup and `existingRouter` implements `http.Handler`. Capture belongs outside framework recovery and final error rendering. Add the matching metadata helper inside Gin, chi, or Echo:

| Application | HTTP wrapper | Route metadata |
| --- | --- | --- |
| `net/http`, `http.ServeMux`, custom `http.Handler` | `httpmw.Middleware(logger)(handler)` | ServeMux patterns are automatic; custom routers can use `apilog.SetRoute`. |
| Gin v1 | Wrap the Gin engine | `apigin.Metadata("api")` from `integrations/gin` |
| chi v5 | Wrap the chi router | `apichi.Metadata("api")` from `integrations/chi` |
| Echo v4 | Wrap the Echo engine | `apiecho.Metadata("api")` from `integrations/echov4` |
| Echo v5 | Wrap the Echo engine | `apiecho.Metadata("api")` from `integrations/echov5` |

The [HTTP integration guide](docs/http-integration.md) includes exact imports, complete helper files, framework-specific middleware order, and runnable examples. Any compatible `http.Handler` can use the wrapper; native Fiber/fasthttp and non-HTTP protocols need their own adapters and are not covered by this release.

For AI coding tools, start with [llms.txt](llms.txt) and the [integration contract](docs/ai-integration.md). They identify package names, supported boundaries, lifecycle rules and verification steps.

## Choose only the packages you need

| Module | Packages |
| --- | --- |
| `github.com/vishalanandl177/go-api-logger` | `apilog`, `httpmw`, `dashboard` |
| `github.com/vishalanandl177/go-api-logger/storage` | PostgreSQL, MySQL, SQLite stores |
| `github.com/vishalanandl177/go-api-logger/integrations` | Frameworks, SQL profiling, Prometheus, OpenTelemetry, Sentry |
| `github.com/vishalanandl177/go-api-logger/cmd` | `apilog` operations command |
| `github.com/vishalanandl177/go-api-logger/examples` | Runnable application and benchmark harness |

Integration dependencies are isolated from the standard-library core. The initial integrations module groups the supported SDKs together; applications import only the adapters they use.

Install optional modules and the operations command independently:

```sh
go get github.com/vishalanandl177/go-api-logger/storage@v1.0.1
go get github.com/vishalanandl177/go-api-logger/integrations@v1.0.1
go install github.com/vishalanandl177/go-api-logger/cmd/apilog@v1.0.1
```

Add the Go binary directory (`go env GOBIN`, or `$(go env GOPATH)/bin` when GOBIN is empty) to your PATH to invoke `apilog`. The CLI reads database credentials from an environment variable. See the [operations guide](docs/operations.md).

For a complete local application with SQLite, profiling, a protected dashboard and metrics, clone this repository and follow [the runnable example](examples/README.md). Framework-specific applications are in [integrations/examples](integrations/examples).

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

- [First request walkthrough](docs/getting-started.md)
- [HTTP, Gin, chi and Echo recipes](docs/http-integration.md)
- [Log format and annotated examples](docs/log-format.md)
- [AI integration contract](docs/ai-integration.md)
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

Recoverable sink errors and logging-hook panics are isolated. Workers continue processing later batches, so logging resumes when a failed output recovers. See [failure isolation and recovery](docs/reliability.md) for tested behavior, custom-sink obligations, and process-level restart boundaries.

This library provides best-effort operational logging. A full queue, output failure, shutdown deadline, or process crash can lose events. Watch `logger.Health()` and `logger.Diagnose()` or the Prometheus integration. `Flush` waits for accepted events to be settled; inspect health counters to distinguish successful delivery from failure.

Custom sinks must honor context cancellation. Go cannot forcibly stop a callback that ignores its context. Each output has one worker, so such a callback cannot create an unbounded goroutine backlog. Observer and policy hooks are synchronous, must be bounded, and must not perform network I/O.

## License

[Apache License 2.0](LICENSE). See [NOTICE](NOTICE).
