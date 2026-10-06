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

Construct an observer with an application-owned registry. Supply finite allowlists of route templates, output names and security rule IDs. Match the framework's actual route string: a method-qualified ServeMux pattern is `GET /users/{id}`, chi uses `/users/{id}`, and Gin or Echo use `/users/:id`. Unknown routes and rule IDs collapse to `other`; unknown output names are ignored. HTTP methods, status classes, severity, stage and outcome labels use fixed enumerations. IDs, IP addresses, raw URLs, bodies, error text and query strings are never metric labels. Each allowlist is capped at 256 entries.

Call this setup function before passing `config` to `apilog.New`. It mounts the protected handler on the application's existing mux:

```go
package example

import (
    "net/http"

    "github.com/prometheus/client_golang/prometheus"
    apilog "github.com/vishalanandl177/go-api-logger"
    apimetrics "github.com/vishalanandl177/go-api-logger/integrations/prometheus"
)

func ConfigureMetrics(config *apilog.Config, mux *http.ServeMux,
    authorizeMetricsRequest func(*http.Request) bool) error {
    registry := prometheus.NewRegistry()
    metrics, err := apimetrics.New(registry, apimetrics.Options{
        Routes: []string{"GET /users/{id}"},
        Outputs: []string{"database"},
        SlowThreshold: config.SlowThreshold,
    })
    if err != nil { return err }
    metricsHandler, err := apimetrics.Handler(registry, authorizeMetricsRequest)
    if err != nil { return err }
    mux.Handle("/internal/metrics", metricsHandler)
    if config.Observer == nil {
        config.Observer = metrics
    } else {
        config.Observer = apilog.ObserverGroup{config.Observer, metrics}
    }
    return nil
}
```

The authorization callback is required and returns whether the caller may scrape metrics. The handler accepts GET and HEAD, sends no-store headers, and automatically excludes its own request from log outputs even at a custom mount path. API metadata metrics still observe these requests. It uses `promhttp.HandlerFor` internally and starts no server. Applications with their own protected endpoint can use that registry directly. The adapter does not use the global registry. Match `SlowThreshold` to the core logger setting.

All five metric groups are enabled by default and can be disabled independently with `DisableAPI`, `DisableProfiling`, `DisableLogger`, `DisablePipeline`, and `DisableSecurity`. Logger metrics cover synchronous capture work and skipped events; pipeline metrics cover asynchronous queue and delivery work. `DisableHealth` remains a convenience option that disables both logger and pipeline groups, regardless of those two individual flags. Existing `apilog_health_*` metric names remain unchanged.

| Group | Metric names |
| --- | --- |
| API | `apilog_api_requests_total`, `apilog_api_duration_seconds`, `apilog_api_active_requests`, `apilog_api_body_bytes`, `apilog_api_slow_requests_total`, `apilog_api_exceptions_total`, `apilog_api_rate_limited_total` |
| SQL profiling | `apilog_profile_query_count`, `apilog_profile_sql_duration_seconds`, `apilog_profile_duplicate_query_count`, `apilog_profile_n_plus_one_hints_total` |
| Logger | `apilog_health_operation_duration_seconds`, `apilog_health_skipped_total` |
| Pipeline | `apilog_health_queue_entries`, `apilog_health_queue_bytes`, `apilog_health_worker_running`, `apilog_health_events_total`, `apilog_health_last_write_seconds`, `apilog_health_batches_total`, `apilog_health_batch_size`, `apilog_health_flush_duration_seconds` |
| Security | `apilog_security_signals_total` |

Histogram families also expose their standard `_bucket`, `_sum` and `_count` series. Body sizes are bytes observed by capture, not inferred client traffic. Exception counts mean observed panics. SQL cumulative duration may exceed request duration because concurrent queries overlap. N+1 signals are suggestions for investigation.

Use one metrics observer per logger lifecycle. Pipeline counters tolerate stale snapshots without counting them twice. Queue depth gauges represent the most recent received snapshot. If batch snapshots are coalesced, batch totals remain exact but the duration and size histograms contain the received samples.

## OpenTelemetry

`integrations/otel.Observer` annotates the existing active span with event/request IDs, route, status, duration and bounded profile summaries. It creates no spans, providers, exporters or network calls. Application tracing middleware must wrap HTTP capture so the span remains active when the event finishes. Configure observers before constructing the logger; changing a copied config afterward has no effect:

```go
package example

import (
    "net/http"

    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
    apiotel "github.com/vishalanandl177/go-api-logger/integrations/otel"
)

func WithTracing(config apilog.Config, router http.Handler,
    existingTracing func(http.Handler) http.Handler) (*apilog.Logger, http.Handler, error) {
    if config.Observer == nil {
        config.Observer = apiotel.Observer{}
    } else {
        config.Observer = apilog.ObserverGroup{config.Observer, apiotel.Observer{}}
    }
    logger, err := apilog.New(config)
    if err != nil { return nil, nil, err }
    handler := existingTracing(httpmw.Middleware(logger)(apiotel.Context(router)))
    return logger, handler, nil
}
```

The caller drains its HTTP server and then calls `logger.Shutdown` as shown in the root quick start. `apiotel.Context` copies the active trace ID into the canonical API event field. Enable `config.Correlation.Enabled` before construction to also generate or accept request IDs. Arbitrary application context, bodies, headers and SQL are excluded from span attributes. The application owns trace sampling and export.

## Sentry

`integrations/sentry.Context` clones the application's hub per request and preserves the configured client and scope. Place it outside capture, or use an existing Sentry HTTP middleware that already supplies a request-local hub. `Observer` only enriches that local scope with an event ID, correlation IDs, route, status, duration and profile count. It never captures or sends an exception.

Register `apisentry.Observer{}` in `config.Observer` before logger construction, composing existing observers with `apilog.ObserverGroup`. Enable `config.Correlation.Enabled` to populate request IDs. Call `apisentry.AttachCorrelation(ctx)` immediately before the request-local hub's `CaptureException` call, using `sentry.GetHubFromContext(ctx)` rather than the global capture function. Final observer enrichment happens after the handler, so it cannot retroactively enrich an event already sent. Bodies, query strings, SQL and arbitrary context fields are not copied. The global Sentry scope is never modified.

## Tests

Run `go test ./...` and `go vet ./...` from `integrations`. `go test -race ./...` checks concurrent core collectors and adapters where the host toolchain supports the race detector. The framework tests exercise outer capture, central error handlers, recovery and 404 responses. See [profiling](profiling.md) for database conformance and optional PostgreSQL test setup.
