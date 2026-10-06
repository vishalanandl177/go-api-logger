# Architecture and adapter contract

The core is a standard-library-only module. Optional storage and integration modules depend on its versioned public interfaces. The embedded dashboard depends only on `Store` and `net/http`.

```text
HTTP/framework observation -> policies -> sanitization -> owned event
                                                       -> bounded queue per sink
                                                       -> optional local observers
```

`httpmw.Middleware(logger)` wraps the framework engine so its final error response is visible. Framework enrichers set route/name/group in shared context before endpoint execution. Existing tracing wraps the capture middleware so local observers can annotate an active span. `SetRoutePattern` supplies standard ServeMux fallback metadata without overwriting native enrichers.

chi uses `SetRouteResolver(ctx, resolver)` to read its current route pattern at each body capture decision. Its deferred finalizer snapshots metadata and waits for active resolver reads before chi can reuse pooled routing state. The innermost metadata registration wins, and an explicit `SetRoute` overrides a resolver. Resolver callbacks run synchronously outside the exchange mutex; they must be fast and must not recursively invoke capture or their finalizer. A resolver panic excludes the exchange and clears retained payloads and inspection samples. Middleware that reads a body before chi selects the endpoint cannot know its final route pattern; use a path or group policy for those early reads, or move body processing after routing.

An additional server adapter creates an `Event` containing request metadata, calls `Begin` to get a context and `Exchange`, observes bodies without pre-reading or delaying output, and calls `Finish` once after the server's response/error rendering. It checks `CaptureAllowed` when reading/writing, uses finite limits, and distinguishes unknown/incomplete/unsupported bodies. It must copy framework-pooled memory before returning. Panics are recorded and propagated; log capture must not implement recovery or invent an HTTP500 response.

Queries and explicit stages attach to the exchange through context. The summary freezes at handler completion; unfinished work is marked incomplete. SQL arguments/results and raw statement strings never enter stored profiles. SQL cumulative duration and interval-union elapsed duration are separate values.

Every output receives an independent owned snapshot after policies and redaction. Each output has one worker and an event/byte budget including in-flight writes. No worker retains a request, response writer, or caller's request context. Caller-owned resources are never closed by logger shutdown.

Observers are bounded synchronous local callbacks for existing telemetry SDKs. They may not perform network I/O. Sinks perform delivery outside request handling and must honor their context deadlines. Failure counters and `Diagnose` make loss visible without failing HTTP requests.

`SkipRequest(ctx)` lets embedded administrative handlers exclude themselves independently of their mount path. Database storage operations use `SuppressProfiling(ctx)` to avoid recursive query measurements.
