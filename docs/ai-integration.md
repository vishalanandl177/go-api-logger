# Integration guide for AI coding tools

This document describes the released v1.0.1 API. Use it with the application's existing architecture and the user's requirements. The [documentation index](../llms.txt) lists the focused guides; [getting started](getting-started.md) contains a runnable first-request workflow.

## Facts to establish in the application

Inspect `go.mod` and server startup before editing code. Identify the router and major version, the handler passed to `http.Server`, the existing shutdown coordinator, output requirements, and application authentication. For profiling, identify the database driver, context propagation, and any existing instrumentation. Keep existing routing, recovery, tracing, and authorization behavior.

| Application boundary | Integration decision |
| --- | --- |
| `net/http` handler, ServeMux, or any type implementing `ServeHTTP(http.ResponseWriter, *http.Request)` | Wrap once with `httpmw.Middleware(logger)(handler)`. |
| Gin v1 | Add `integrations/gin.Metadata` inside Gin and wrap the engine outside Gin. |
| chi v5 | Add `integrations/chi.Metadata` with `Use`, then wrap the router outside chi. |
| Echo v4 | Use `integrations/echov4.Metadata` and wrap the Echo engine outside central error rendering. |
| Echo v5 | Use `integrations/echov5.Metadata`; do not mix v4 and v5 context types. |
| Native Fiber/fasthttp or another non-net/http transport | No shipped native adapter. Explain the boundary; do not invent a `fiber` package or promise conformance through a bridge. |
| Non-HTTP events | Custom `Event` submission is a lower-level API, not automatic request/response observation. See [architecture](architecture.md) before writing an adapter. |

The [HTTP guide](http-integration.md) provides compilable helper files and runnable framework examples. A framework metadata helper does not create an event on its own.

## Exact package and module names

The import alias `apilog` refers to the root package, not a `/apilog` subdirectory. Optional packages are in separate versioned modules.

| Need | Import | Module to install |
| --- | --- | --- |
| Config, Logger, Event, sinks, context helpers | `apilog "github.com/vishalanandl177/go-api-logger"` | `github.com/vishalanandl177/go-api-logger@v1.0.1` |
| HTTP capture | `github.com/vishalanandl177/go-api-logger/httpmw` | Root module |
| Dashboard handler | `github.com/vishalanandl177/go-api-logger/dashboard` | Root module |
| PostgreSQL/MySQL/SQLite | `github.com/vishalanandl177/go-api-logger/storage` | `github.com/vishalanandl177/go-api-logger/storage@v1.0.1` |
| Gin, chi, Echo, profiling, telemetry | `github.com/vishalanandl177/go-api-logger/integrations/<package>` | `github.com/vishalanandl177/go-api-logger/integrations@v1.0.1` |

Supported integration package suffixes are `gin`, `chi`, `echov4`, `echov5`, `sql`, `pgx`, `gorm`, `prometheus`, `otel`, and `sentry`. The root `go.mod` requires Go 1.26.0; CI tests Go 1.26 and 1.27. Do not copy this repository's multi-module `go work` development setup into a consumer application. Install its published modules normally.

## Construction and lifecycle

1. Start with `cfg := apilog.DefaultConfig()`.
2. Set `cfg.Outputs` to one or more named `apilog.Output` values. `Kind` is `storage` or `export`. An empty output list writes no logs; metrics-only use requires an observer.
3. Configure policies, correlation, profiling and observers before `apilog.New(cfg)`. Handle its error. Config is copied; editing the original config after construction does not reconfigure a running logger.
4. Use one logger for the application lifetime. Install framework metadata where applicable, then wrap the final `http.Handler` once.
5. Let existing tracing surround capture when enriching its active span. Let capture surround framework recovery/error rendering so it sees the final response.
6. On application shutdown, first stop accepting and drain HTTP requests, then call `logger.Shutdown` with a fresh bounded context, then close caller-owned sinks/database pools. Do not reuse the already-canceled signal context for draining.

Key signatures, simplified to the public names:

```text
apilog.DefaultConfig() Config
apilog.New(Config) (*Logger, error)
httpmw.Middleware(*Logger) func(http.Handler) http.Handler
Sink.WriteBatch(context.Context, []Event) error
Logger.Flush(context.Context) error
Logger.Shutdown(context.Context) error
Logger.Health() Health
Logger.Diagnose() []Diagnostic
```

See [types.go](../types.go), [logger.go](../logger.go), and [diagnostics.go](../diagnostics.go) for authoritative types. Never substitute guessed APIs such as `logger.Use(router)`, `apilog.NewMiddleware`, or a Gin-specific capture middleware.

## Privacy and observation constraints

- Default capture is JSON, bounded to 32 KiB request / 64 KiB response. Capture limits are not application request-size limits; use the application's own body limit too.
- Observe bodies only as the application consumes/writes them. Do not pre-read or drain a request solely to make logging complete. Explain `unread`, `incomplete`, `invalid`, and `oversized` states using the [log format](log-format.md).
- Add application secrets to `cfg.MaskKeys`. Built-in keys mask common credentials recursively in JSON, headers, and query values; arbitrary secret values in other fields/paths are not magically detected.
- Apply path or other available capture restrictions before any middleware reads a body. A final status rule can restrict emission but cannot make an earlier read disappear.
- Do not queue framework contexts or pooled buffers. Do not log raw bodies as a fallback when sanitization fails.
- `SetContext` accepts application-supplied fields. Explicitly select approved opaque values; do not copy all context/session/user fields or use identifiers as metric labels.
- Keep proxy trust explicit. Direct peer addresses are used until `TrustedProxies` is configured. Never configure all addresses as trusted just to obtain a forwarded client IP.

## Storage, dashboard and profiling

`storage.OpenSQLite`, `OpenPostgres`, and `OpenMySQL` return a store, a caller-owned `*sql.DB`, and an error. They do not migrate. Run migrations as an explicit deployment/bootstrap operation, then use `Store.Check` when starting workers. Use a separate log database/pool from instrumented application queries. [Storage examples](storage.md) show the exact signatures and DSN handling.

`dashboard.New(store, options)` returns a handler and an error. Supply application authorization separately for view, export, and delete, mount the handler behind existing authentication, and retain its protected POST deletion flow. Do not generate a production callback that returns `true` for every request. [Dashboard examples](dashboard.md) show how to map application permissions.

Profiling is disabled by default. Enable `cfg.Profile.Enabled`, instrument exactly one source per database path, and pass request context to SQL, pgx, or GORM. Do not combine a wrapped SQL driver, pgx tracer, and GORM logger on the same path. Preserve existing SDK hooks as documented in [profiling](profiling.md). SQL cumulative work is not elapsed request time.

Use [Prometheus/OpenTelemetry/Sentry integrations](integrations.md) with application-owned registries, spans, hubs and exporters. Prometheus labels use finite route templates and allowlists. Do not start an exporter or another public server merely to integrate logging.

## Verification before calling an integration complete

Read [failure isolation and recovery](reliability.md) before adding failure handling. Do not invent unbounded retry loops, replacement-worker goroutines, or process restarts inside request middleware. Keep failed-batch accounting visible and use the application's service supervisor for process failures.

| Check | Expected evidence |
| --- | --- |
| Build and existing tests | Application still compiles with its installed framework major version. |
| Request/response capture | Send a valid JSON request that the handler reads; one event has the final status and response. |
| Privacy | A fictional nested credential and Authorization header are masked in emitted/stored logs. |
| Route metadata | Stored route is a template, not `/users/42`; match metric allowlists to the exact adapter route string. |
| Framework errors | A 404 and a framework-rendered error are captured with final response metadata. |
| Shutdown | Stop server, drain logger, then close output resources; inspect delivery counters. |
| Delivery health | `Dropped`, `Failed`, `Queued`, and `InFlight` are examined; HTTP 200 alone is not proof of log persistence. |
| Dashboard, if enabled | Anonymous access denied; export/delete permissions checked independently. |
| Profiling, if enabled | One context-aware application query appears once; logger storage queries do not inflate the profile. |

`Flush` waits for accepted work to settle, not for every request to be successfully stored. Delivery is best effort; queue overflow, write failures, process exits, and shutdown deadlines can lose logs. Increasing queue capacity only absorbs a temporary burst. It does not make a persistently slower database keep up.

## Example request for a coding assistant

```text
Integrate github.com/vishalanandl177/go-api-logger v1.0.1 into this application.
Read go.mod and the existing server/lifecycle/auth setup first.
Use README.md, llms.txt, docs/http-integration.md and docs/ai-integration.md from
the library as API references. Keep the existing router and server.
Start with a JSON stdout sink and explicit privacy exclusions, add the correct
framework metadata helper, and wrap HTTP capture exactly once outside recovery.
Use the application's shutdown coordinator. Show the changed startup code,
the install commands, and one masked request/response event from a test.
Do not add a database, dashboard, SQL profiling or telemetry exporter unless
requested. State any unsupported transport or unverified behavior explicitly.
```
