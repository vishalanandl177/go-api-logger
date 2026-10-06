# Framework and observability integrations

The root module has no SDK dependencies. Optional adapters are in the separate `github.com/vishalanandl177/go-api-logger/integrations` module. Import only the packages your application needs. Framework middleware enriches the single event produced by outer `httpmw.Middleware`.

## Frameworks

| Framework | Package | Integration |
| --- | --- | --- |
| Standard library | `httpmw` in root | Wrap any `http.Handler`; ServeMux route patterns are recognized. |
| chi v5 | `integrations/chi` | `router.Use(apichi.Metadata("group"))` |
| Gin v1 | `integrations/gin` | `router.Use(apigin.Metadata("group"))` |
| Echo v4 | `integrations/echov4` | `router.Use(apiecho.Metadata("group"))` |
| Echo v5 | `integrations/echov5` | Same shape, with Echo v5 context types and named route metadata. |

Pass `httpmw.Middleware(logger)(router)` to `http.Server.Handler`. The outer wrapper sees Echo's final error responses and framework recovery responses. Installing metadata alone does not capture requests. Do not install a second HTTP capture middleware inside the same router. A framework recovery middleware may consume a panic before outer capture sees it; the response status is recorded, but a 500 status is not proof of an exception.

Gin records the handler name separately from route names. Echo v5 reads the registered route name. Use root `SetRoute`, `SetHandler`, and `SetContext` for explicit application metadata. Framework contexts are used only synchronously and are never queued.

Runnable examples, from the `integrations` directory:

```sh
go run ./examples/gin
go run ./examples/chi
go run ./examples/echov4
go run ./examples/echov5
```

Run one at a time. Each listens on loopback port 8080, serves `/users/42`, writes sanitized JSON lines, and drains HTTP requests before flushing logs on interrupt.

The integration module pins verified Go proxy versions: Gin 1.12.0, chi 5.3.2, Echo 4.16.0 and 5.4.0. Other net/http-compatible routers can use the generic wrapper directly. Fiber/fasthttp require a native adapter or their interoperability bridge; these packages do not claim native Fiber/fasthttp conformance.

## Prometheus

Construct an observer with an application-owned registry. Supply finite allowlists of route templates, output names and security rule IDs. Unknown routes and rule IDs collapse to `other`; unknown output names are ignored. HTTP methods, status classes, severity, stage and outcome labels use fixed enumerations. IDs, IP addresses, raw URLs, bodies, error text and query strings are never metric labels. Each allowlist is capped at 256 entries.

```go
registry := prometheus.NewRegistry()
metrics, err := apimetrics.New(registry, apimetrics.Options{
    Routes: []string{"/users/{id}"},
    Outputs: []string{"database"},
    SlowThreshold: 200 * time.Millisecond,
})
if err != nil { return err }
config.Observer = metrics
```

Expose that registry through the application's existing authenticated metrics endpoint using `promhttp.HandlerFor`. The adapter does not register an HTTP endpoint or use the global registry. Match `SlowThreshold` to the core logger setting. Disable groups independently with `DisableAPI`, `DisableProfiling`, `DisableHealth`, and `DisableSecurity`.

| Group | Metric names |
| --- | --- |
| API | `apilog_api_requests_total`, `apilog_api_duration_seconds`, `apilog_api_active_requests`, `apilog_api_body_bytes`, `apilog_api_slow_requests_total`, `apilog_api_exceptions_total`, `apilog_api_rate_limited_total` |
| SQL profiling | `apilog_profile_query_count`, `apilog_profile_sql_duration_seconds`, `apilog_profile_duplicate_query_count`, `apilog_profile_n_plus_one_hints_total` |
| Pipeline health | `apilog_health_queue_entries`, `apilog_health_queue_bytes`, `apilog_health_worker_running`, `apilog_health_events_total`, `apilog_health_last_write_seconds`, `apilog_health_operation_duration_seconds`, `apilog_health_skipped_total`, `apilog_health_batches_total`, `apilog_health_batch_size`, `apilog_health_flush_duration_seconds` |
| Security | `apilog_security_signals_total` |

Histogram families also expose their standard `_bucket`, `_sum` and `_count` series. Body sizes are bytes observed by capture, not inferred client traffic. Exception counts mean observed panics. SQL cumulative duration may exceed request duration because concurrent queries overlap. N+1 signals are suggestions for investigation.

Use one metrics observer per logger lifecycle. Pipeline counters tolerate stale snapshots without counting them twice. Queue depth gauges represent the most recent received snapshot. If batch snapshots are coalesced, batch totals remain exact but the duration and size histograms contain the received samples.

## OpenTelemetry

`integrations/otel.Observer` annotates the existing active span with event/request IDs, route, status, duration and bounded profile summaries. It creates no spans, providers, exporters or network calls. Application tracing middleware must wrap HTTP capture so the span remains active when the event finishes:

```go
config.Observer = apilog.ObserverGroup{metrics, apiotel.Observer{}}
handler := existingTracing(httpmw.Middleware(logger)(apiotel.Context(router)))
```

`apiotel.Context` copies the active trace ID into the canonical API event field. Arbitrary application context, bodies, headers and SQL are excluded from span attributes. The application owns trace sampling and export.

## Sentry

`integrations/sentry.Context` clones the application's hub per request and preserves the configured client and scope. Place it outside capture, or use an existing Sentry HTTP middleware that already supplies a request-local hub. `Observer` only enriches that local scope with an event ID, correlation IDs, route, status, duration and profile count. It never captures or sends an exception.

Call `apisentry.AttachCorrelation(ctx)` immediately before an application's own `CaptureException` call. Final observer enrichment happens after the handler, so it cannot retroactively enrich an event already sent. Bodies, query strings, SQL and arbitrary context fields are not copied. The global Sentry scope is never modified.

## Tests

Run `go test ./...` and `go vet ./...` from `integrations`. `go test -race ./...` checks concurrent core collectors and adapters where the host toolchain supports the race detector. The framework tests exercise outer capture, central error handlers, recovery and 404 responses. See [profiling](profiling.md) for database conformance and optional PostgreSQL test setup.
