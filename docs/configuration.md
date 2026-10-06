# Configuration and lifecycle

Construct configuration with `apilog.DefaultConfig()`. Constructors reject invalid finite limits, duplicate output names, unsupported path modes, invalid proxy CIDRs, and invalid profiling/security bounds. Configuration slices are copied; treat hook implementations as immutable and concurrency-safe.

## Capture and policy

`MetadataOnly` omits request and response bodies. `Methods`, `Statuses`, `SkipRoutes`, `SkipNames`, `SkipGroups`, and `SkipPaths` control selection. Path prefixes match the path itself and descendants. `PathMode` is `full` (path/query), `path`, or `absolute` (scheme/host/path/query). URL user information and fragments are never retained.

```go
cfg := apilog.DefaultConfig()
cfg.SkipPaths = []string{"/healthz", "/metrics", "/api-logs"}
cfg.MaskKeys = []string{"card_number", "customer_secret"}
cfg.Rules = []apilog.Rule{{
    Route: "POST /payments",
    Decision: apilog.Decision{
        StripHeaders: true, StripRequest: true, StripResponse: true,
        DisableExport: true,
    },
}}
```

Rules are evaluated in order and combine restrictively: a later rule cannot re-enable a field or destination already disabled by an earlier rule. Masks are additive. Match route, name, group, handler, path prefix, method, status, or status class. Native router enrichers must run before the endpoint to make route policies available while a request body is read. Status-specific rules are evaluated only after response completion and cannot prevent the earlier bounded observation of a body.

`Policy func(Event) (Decision, error)` is evaluated during capture and after completion. It must handle a zero status during capture. Errors/panics strip headers/bodies/query and disable export, while safe metadata can still be persisted. `Transform` sees sanitized data and may return nil to drop an event. Transformed fields pass through redaction again. Persisted `ExportDisabled` also blocks dashboard export.

Bodies have explicit `complete`, `empty`, `unread`, `incomplete`, `oversized`, `invalid`, `unsupported`, `encoded`, `file`, `streaming`, `upgraded`, `header_only`, or `omitted` states. Bytes represent observed bytes, not always the original full payload size. Compressed, file, multipart and streaming content is not decompressed or buffered for logging. HEAD responses retain no body bytes because no response payload is transmitted. `Body.Encoding="json"` marks canonical sanitized JSON while `ContentType` retains the original media type. A custom `Sanitizer` can support additional explicitly configured content types, but must return valid sanitized JSON; built-in sensitive-key masking is applied to that JSON too.

## Custom outputs

Implement `Sink.WriteBatch(context.Context, []Event) error` or use `SinkFunc`. Each output has an independent bounded queue. Events are owned snapshots; a sink can retain its batch. The sink must honor cancellation and return errors without changing HTTP responses. No automatic retries are made, including for callbacks.

Use output kind `storage` for persistent local records or `export` for subscribers. Policies gate these independently. A logger may have no outputs when only metrics are required.

`Observer` receives sanitized events and health snapshots synchronously. Optional `RequestObserver`, `TimingObserver`, and `SkipObserver` hooks support metrics. Observers must be fast and avoid I/O; use a sink for outbound delivery. `ObserverGroup` combines observers and isolates their panics.

## Shutdown and resources

Stop accepting HTTP requests and wait for in-flight handlers first. Then call `logger.Shutdown(ctx)` with an explicit deadline, and finally close application-owned database pools/files. Shutdown never installs signal handlers or closes caller-owned resources. Calls are idempotent; events submitted after shutdown are not accepted.

`Flush(ctx)` waits for events accepted before its snapshot to be settled. It does not promise persistence after a sink error; examine `Health()` counters. Failed writes are counted and discarded. The byte budget includes the in-flight batch; the stored value measures serialized event bytes, while Go object/queue overhead consumes additional bounded memory.

## Correlation

Enable `cfg.Correlation.Enabled`. Valid incoming request IDs come from `X-Request-ID` or `X-Correlation-ID`, otherwise a random ID is generated. Configure `GenerateID` to customize generation. Trace IDs are read from validated W3C `traceparent` or configured trace headers; the OpenTelemetry adapter can attach the active span's ID.

Use `RequestID(ctx)`, `TraceID(ctx)`, `SetContext(ctx, allowlistedValues)`, and `WithCorrelation(ctx, slogLogger)` in application code. Supply only opaque actor/tenant/client identifiers and explicitly allowed business context. These values are not metric labels.
