# Configuration and lifecycle

Construct configuration with `apilog.DefaultConfig()`. Constructors reject invalid finite limits, duplicate output names, unsupported path modes, invalid proxy CIDRs, and invalid profiling/security bounds. Configuration slices are copied; treat hook implementations as immutable and concurrency-safe.

Install the core module with `go get github.com/vishalanandl177/go-api-logger@v1.0.1`. All examples on this page use that module alone. Create one logger during application startup, pass it to your [HTTP or framework adapter](integrations.md), and drain it during shutdown. Mutating `cfg` after `apilog.New(cfg)` does not reconfigure the running logger.

| Setting | Default | What to change |
| --- | --- | --- |
| Outputs | None | Add at least one sink to retain logs. With no output, only configured observers receive events. |
| Request / response body limit | 32 KiB / 64 KiB | Finite observation limits, independent of the server's upload limit. |
| Queue capacity / bytes | 1,024 events / 16 MiB per output | Admission stops at either bound, including work in flight. |
| Batch size / flush interval | 50 events / 10 seconds | Low-volume logs may remain queued until the interval elapses. |
| Write timeout | 5 seconds | Each sink must cooperate with context cancellation. |
| Profiling / correlation / security | Disabled | Enable the features you need explicitly. |
| Client address | Direct connection peer | Configure only CIDRs belonging to your actual trusted reverse proxies. |

The queue settings apply independently to every output. Extra outputs add their own memory budgets. Capture limits do not reject a request or truncate the application's stream; use `http.MaxBytesReader` or the framework's request-size control to enforce an upload limit.

## Capture and policy

`MetadataOnly` omits request and response bodies. `Methods` and `Statuses` are inclusion lists; empty lists allow every method/status. `SkipRoutes`, `SkipNames`, `SkipGroups`, and `SkipPaths` exclude matches. `SkipPaths` matches the path itself and descendants; `/health` excludes `/health/live` but not `/healthcare`. In contrast, `Rule.PathPrefix` is a literal string prefix; `/payments` also matches `/payments-export`. Use an exact route rule or a `Policy` callback when that distinction matters.

`PathMode` is `full` (path/query), `path`, or `absolute` (scheme/host/path/query). URL user information and fragments are never retained. Key masking applies recursively to JSON, headers, and query parameters, but does not recognize arbitrary secrets in path segments or free text. Avoid putting secrets in URL paths.

```go
package app

import (
    "net/http"
    "time"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func LoggingConfig(outputs []apilog.Output) apilog.Config {
    cfg := apilog.DefaultConfig()
    cfg.Outputs = outputs
    cfg.Queue.FlushInterval = time.Second
    cfg.SkipPaths = []string{"/healthz", "/metrics", "/api-logs"}
    cfg.Methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
    cfg.MaskKeys = []string{"card_number", "customer_secret"}
    cfg.Rules = []apilog.Rule{{
        // This string matches net/http ServeMux's method-aware route pattern.
        // For other routers, use the route format in the framework guide.
        Route: "POST /payments",
        Decision: apilog.Decision{
            StripHeaders: true, StripRequest: true, StripResponse: true,
            StripQuery: true, DisableExport: true,
        },
    }}
    return cfg
}
```

Rules are evaluated in order and combine restrictively: a later rule cannot re-enable a field or destination already disabled by an earlier rule. Masks are additive. Match route, name, group, handler, path prefix, method, status, or status class. Native router enrichers must run before the endpoint to make route policies available while a request body is read. Status-specific rules are evaluated only after response completion and cannot prevent the earlier bounded observation of a body.

Built-in masks include `password`, `token`, `authorization`, `cookie`, `set_cookie`, `api_key`, and `secret`. Matching ignores key case and treats hyphens as underscores. `MaskKeys` adds to these defaults. For example, `{"user":{"password":"demo","card_number":"sample"}}` becomes `{"user":{"password":"***FILTERED***","card_number":"***FILTERED***"}}` with the configuration above. `MetadataOnly` alone still retains sanitized headers and URLs; add `StripHeaders` and `StripQuery` when those should be omitted too.

`Policy func(Event) (Decision, error)` is evaluated during capture and after completion. It must handle a zero status during capture. Errors/panics strip headers/bodies/query and disable export, while safe metadata can still be persisted. `Transform` sees sanitized data and may return nil to drop an event. Transformed fields pass through redaction again. Persisted `ExportDisabled` also blocks dashboard export.

Bodies have explicit `complete`, `empty`, `unread`, `incomplete`, `oversized`, `invalid`, `unsupported`, `encoded`, `file`, `streaming`, `upgraded`, `header_only`, or `omitted` states. Bytes represent observed bytes, not always the original full payload size. Compressed, file, multipart and streaming content is not decompressed or buffered for logging. HEAD responses retain no body bytes because no response payload is transmitted. `Body.Encoding="json"` marks canonical sanitized JSON while `ContentType` retains the original media type. A custom `Sanitizer` can support additional explicitly configured content types, but must return valid sanitized JSON; built-in sensitive-key masking is applied to that JSON too.

## Custom outputs

Implement `Sink.WriteBatch(context.Context, []Event) error` or use `SinkFunc`. Each output has an independent bounded queue. Events are owned snapshots; a sink can retain its batch. The sink must honor cancellation and return errors without changing HTTP responses. No automatic retries are made, including for callbacks.

Use output kind `storage` for persistent local records or `export` for subscribers. Policies gate these independently. A logger may have no outputs when only metrics are required.

This helper shows JSON lines, `slog`, and a custom batch subscriber together. Pass nil for an output you do not want. `jsonWriter` might be `os.Stdout` or an application-owned file. `structured` might be `slog.New(slog.NewJSONHandler(os.Stdout, nil))`. Choosing both stdout outputs intentionally writes two representations of each event.

```go
package app

import (
    "context"
    "io"
    "log/slog"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func OutputLogger(
    jsonWriter io.Writer,
    structured *slog.Logger,
    deliver func(context.Context, []apilog.Event) error,
) (*apilog.Logger, error) {
    cfg := apilog.DefaultConfig()
    if jsonWriter != nil {
        cfg.Outputs = append(cfg.Outputs, apilog.Output{
            Name: "json", Kind: "export", Sink: &apilog.JSONSink{Writer: jsonWriter},
        })
    }
    if structured != nil {
        cfg.Outputs = append(cfg.Outputs, apilog.Output{
            Name: "slog", Kind: "export", Sink: apilog.SlogSink{Logger: structured},
        })
    }
    if deliver != nil {
        cfg.Outputs = append(cfg.Outputs, apilog.Output{
            Name: "subscriber", Kind: "export", Sink: apilog.SinkFunc(deliver),
        })
    }
    return apilog.New(cfg)
}
```

The subscriber receives sanitized events on its own worker, in batches up to `Queue.BatchSize`. Pass its supplied `ctx` to outbound HTTP/database calls. A write error marks the whole batch failed; partial external writes may already have happened. There are no automatic retries. A plain `io.Writer` or `slog.Handler` can block despite cancellation, so prefer a context-aware custom sink for network transports. File rotation, permissions, sync, and close belong to the application.

`Observer` receives sanitized events and health snapshots synchronously. Optional `RequestObserver`, `TimingObserver`, and `SkipObserver` hooks support metrics. Observers must be fast and avoid I/O; use a sink for outbound delivery. `ObserverGroup` combines observers and isolates their panics.

## Shutdown and resources

Stop accepting HTTP requests and wait for in-flight handlers first. Then call `logger.Shutdown(ctx)` with an explicit deadline, and finally close application-owned database pools/files. Shutdown never installs signal handlers or closes caller-owned resources. Calls are idempotent; events submitted after shutdown are not accepted.

`Flush(ctx)` waits for events accepted before its snapshot to be settled. It does not promise persistence after a sink error; examine `Health()` counters. Failed writes are counted and discarded. The byte budget includes the in-flight batch; the stored value measures serialized event bytes, while Go object/queue overhead consumes additional bounded memory.

## Correlation

Enable `cfg.Correlation.Enabled`. Valid incoming request IDs come from `X-Request-ID` or `X-Correlation-ID`, otherwise a random ID is generated. Configure `GenerateID` to customize generation. Trace IDs are read from validated W3C `traceparent` or configured trace headers; the OpenTelemetry adapter can attach the active span's ID.

Use `RequestID(ctx)`, `TraceID(ctx)`, `SetContext(ctx, allowlistedValues)`, and `WithCorrelation(ctx, slogLogger)` in application code. Supply only opaque actor/tenant/client identifiers and explicitly allowed business context. These values are not metric labels.

`SetContext` does not have a configurable allowlist: your application chooses the allowed keys and values before calling it. Do not copy all headers, session fields, or request bodies into that map. Incoming request IDs correlate work; they do not authenticate the caller. The logger does not automatically write a response request-ID header.

```go
package app

import (
    "log/slog"
    "net/http"

    apilog "github.com/vishalanandl177/go-api-logger"
)

// Install inside capture: httpmw.Middleware(logger)(CorrelationHeaders(mux)).
// Construct logger with cfg.Correlation.Enabled = true.
func CorrelationHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if id := apilog.RequestID(r.Context()); id != "" {
            w.Header().Set("X-Request-ID", id)
        }
        apilog.WithCorrelation(r.Context(), slog.Default()).DebugContext(r.Context(), "request started")
        next.ServeHTTP(w, r)
    })
}

// Call only after application authentication has resolved an opaque tenant ID.
func AttachTenant(r *http.Request, opaqueTenantID string) {
    apilog.SetContext(r.Context(), map[string]string{"tenant_id": opaqueTenantID})
}
```

For a complete server, request generation, database setup, and shutdown flow, see the [standard application](../examples/standard/main.go) and [operations guide](operations.md).
