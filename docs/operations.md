# Operations

Install the released operations CLI without cloning this repository:

```sh
go install github.com/vishalanandl177/go-api-logger/cmd/apilog@v1.0.1
```

Go places `apilog` (or `apilog.exe` on Windows) in `GOBIN`, or in `$(go env GOPATH)/bin` when `GOBIN` is unset. Add that directory to PATH. For a local SQLite log file, run:

```sh
export API_LOGGER_DSN='./api-logs.db'
apilog migrate --driver sqlite
apilog doctor --driver sqlite --format json
apilog prune --driver sqlite --days 30 --dry-run
```

PowerShell equivalent:

```powershell
$env:API_LOGGER_DSN = './api-logs.db'
apilog.exe migrate --driver sqlite
apilog.exe doctor --driver sqlite --format json
apilog.exe prune --driver sqlite --days 30 --dry-run
```

Run these from the directory containing the intended database, or use an absolute path. A relative SQLite path is relative to the CLI's current directory, which may differ from the server's working directory. To apply retention after reviewing the dry-run result, rerun the same prune command without `--dry-run`.

When working from a clone, build the optional CLI from its module instead:

```sh
cd cmd
mkdir -p ../.artifacts
go build -o ../.artifacts/apilog ./apilog
export API_LOGGER_DSN='api-logs.db'
../.artifacts/apilog migrate --driver sqlite
../.artifacts/apilog doctor --driver sqlite --format json
../.artifacts/apilog prune --driver sqlite --days 30 --dry-run
../.artifacts/apilog prune --driver sqlite --days 30
```

PowerShell uses `New-Item -ItemType Directory -Force ../.artifacts`, `$env:API_LOGGER_DSN = 'api-logs.db'` and `..\.artifacts\apilog.exe` after building `go build -o ../.artifacts/apilog.exe ./apilog`. Use `--driver postgres` or `--driver mysql` with the corresponding driver DSN. `--dsn-env NAME` reads a different environment variable. Connection strings are not accepted as positional arguments and are never printed in diagnostics. The CLI opens and closes its own pool; library callers retain ownership of theirs.

## Migration and readiness

`migrate` is explicit and versioned. Run it once as part of deployment before enabling database logging or opening the dashboard. The process needs schema creation privileges. Runtime applications can use a narrower account with the required read/write permissions. A later schema version fails readiness instead of being silently downgraded.

`doctor` checks connectivity, exact schema version, and required columns without reading payloads or changing tables. It emits coded `ok`, `warning`, or `error` results in text or JSON. `--fail-level error` is the default; `--fail-level warning` also fails on warnings. An error exits 1; invalid command options exit 2; successful commands exit 0. `--timeout 30s` bounds the entire database operation and can be changed.

For SQLite, `doctor` and `prune --dry-run` open an existing file with `mode=ro`, so a missing database is never created. Diagnostic DSNs accept plain filenames or `file:` URIs and only the `mode` and `cache` parameters. Supplied writable modes are replaced with `ro`; memory databases, duplicate parameters, `_pragma`, driver shorthand options and other parameters are rejected before opening. Use a separate DSN without connection-time configuration for these diagnostic commands.

A standalone doctor process cannot inspect a running application's worker, queue, policies, body limits or profiling configuration. It explicitly reports `RUNTIME_NOT_INSPECTED` as a warning. Inspect the application's in-process `logger.Health()` together with the configuration validated by logger construction. Monitor worker availability, accepted/delivered/dropped/failed counts, queued events/bytes, in-flight batches and write duration. The CLI does not start a dummy worker or report its health as the application's health.

## Inspect the live logger

This handler exposes the logger's actual counters and diagnostics. Mount it behind your application's authentication, for example at `/internal/logger-health`, and add that prefix to `cfg.SkipPaths` before `apilog.New`. It returns diagnostic data; it intentionally does not equate every historical dropped log with an unhealthy customer-facing API.

```go
package app

import (
    "encoding/json"
    "net/http"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func LoggerHealth(logger *apilog.Logger) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet {
            w.Header().Set("Allow", http.MethodGet)
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        w.Header().Set("Cache-Control", "no-store")
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(struct {
            Health apilog.Health `json:"health"`
            Diagnostics []apilog.Diagnostic `json:"diagnostics"`
        }{logger.Health(), logger.Diagnose()})
    })
}
```

Health is a per-output snapshot. Counters are cumulative for this logger instance and reset when the process restarts:

| Field | Interpretation |
| --- | --- |
| `Accepted` | Events admitted to this output's queue. |
| `Delivered` | Events in batches for which the sink returned nil. |
| `Dropped` | Events rejected at capacity/serialization, or queued work discarded during deadline expiry. |
| `Failed` | Events in batches whose write failed or panicked. |
| `Queued` / `InFlight` | Events waiting / currently being written. |
| `QueuedBytes` | Serialized bytes admitted but not settled, including the in-flight batch. |
| `WorkerRunning` | Whether the output worker is still running. |
| `Skipped` on `Health` | Global skips from policies or transformation hooks. |

Use counter deltas to calculate rates. Do not add `Accepted` across outputs and interpret it as a unique request count: the same event may go to multiple outputs. `Delivered` proves the sink returned success, with the durability guarantees of that sink; it is not a universal disk-sync guarantee.

For tests or an administrator-triggered drain, call `Flush` with a fresh deadline. It waits for the events accepted before the call, while the logger continues accepting new requests. Failure and dropping are accounted for separately from its return value:

```go
package app

import (
    "context"
    "time"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func FlushSnapshot(logger *apilog.Logger) (apilog.Health, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    err := logger.Flush(ctx)
    return logger.Health(), err
}
```

Do not call `Flush` on every request or from every readiness probe. It is an explicit synchronization point and defeats the latency advantage of asynchronous delivery if placed on the request path.

## Retention

Prune supports exactly one of `--days N` or `--before YYYY-MM-DD/RFC3339`. Calendar dates are interpreted as midnight UTC. The cutoff is exclusive, so an event exactly at the cutoff survives. Use `--dry-run` first, and choose a retention period that matches the data owner's requirements. `--batch-size` defaults to 1000 and accepts 1 through 1000. Schedule the command using the application's deployment scheduler; the library does not install a system job.

Deletion is committed batch by batch and can be interrupted. Reports include the initial matching row count, actual deleted count, and dry-run mode. A failed run can have deleted earlier batches; rerun with the same cutoff after investigating. Backups and replicas may retain data after primary-row deletion and require their own retention controls.

The equivalent library API is `store.Prune(ctx, apilog.PruneOptions{Before: cutoff, BatchSize: 1000, DryRun: true})`. It returns `apilog.PruneResult{Matched, Deleted}` and an error. Keep the same `cutoff` when applying an approved preview with `DryRun: false`. Choose it explicitly in UTC, and use a deadline-bound context. No logger worker or HTTP middleware automatically schedules retention.

## Delivery and shutdown

The application owns HTTP server shutdown, logger draining and pool closure. Stop accepting HTTP requests, allow existing requests to finish, call the logger's deadline-bound shutdown, then close database pools. Never close a pool while its logger worker is still writing. A forced process exit can lose queued events.

This helper belongs in your application's signal/shutdown path. Pass the HTTP server, its logger, and the application-owned `*sql.DB` pools, including the log pool. Native pgx pools and files can be closed after this function succeeds. Do not call it from a request handler, which would wait for itself to finish.

```go
package app

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "net/http"
    "time"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func DrainAndClose(server *http.Server, logger *apilog.Logger, pools ...*sql.DB) error {
    httpCtx, cancelHTTP := context.WithTimeout(context.Background(), 20*time.Second)
    err := server.Shutdown(httpCtx)
    cancelHTTP()
    if err != nil {
        // Active handlers may still use the logger and database pools.
        return fmt.Errorf("HTTP drain incomplete: %w", err)
    }

    logCtx, cancelLogger := context.WithTimeout(context.Background(), 10*time.Second)
    err = logger.Shutdown(logCtx)
    cancelLogger()
    if err != nil {
        // Inspect Health. A sink that ignores cancellation may still be running.
        return fmt.Errorf("logger drain incomplete: %w", err)
    }

    var closeErrors []error
    for _, db := range pools {
        if db != nil { closeErrors = append(closeErrors, db.Close()) }
    }
    return errors.Join(closeErrors...)
}
```

Use fresh shutdown contexts derived from `context.Background()`, not the already canceled signal context. If either drain times out, choose an application-specific recovery/termination policy and inspect health; the example leaves pools open while work may still reference them. HTTP shutdown also does not wait for hijacked connections such as WebSockets: applications own their connection lifecycle. `Shutdown` is safe to call again, but a deadline-expired shutdown has already canceled output contexts and may have discarded queued events; a later call cannot restore them.

Delivery is best-effort. Queue saturation and database outages can drop logs; the logger does not block customer requests waiting for database recovery. Alert on growing queues, dropped/failed events and worker failure. Use another system when durable audit delivery is a requirement. Keep storage writes separate from the application's query profiling and protect metrics/dashboard endpoints with application authorization.

SQLite is suitable for local/single-instance use with a local file and serialized writes. Concurrent processes, network filesystems, sustained ingestion and broad payload searches need deployment-specific testing. PostgreSQL or MySQL is usually the appropriate choice for a shared operational log store. Configure connection pool limits, TLS, backup, encryption and database access within the application and infrastructure.

At high QPS, measure API throughput/latency and delivered-log throughput separately. Increasing queue capacity absorbs bursts; it does not increase steady-state database throughput. Compare `Accepted`, `Delivered`, `Dropped`, and `Failed` before and after each workload, allow the queue to drain, and record body sizes, output kind, database, concurrency, and profiling settings. See the [benchmark instructions](performance.md) for reproducible workload modes and the [standard example](../examples/standard/main.go) for a complete lifecycle.
