# What a log record looks like

Each captured HTTP exchange produces an `apilog.Event`. `JSONSink` writes one compact JSON object per line. SQL storage saves the sanitized event together with searchable/indexed fields. The dashboard formats the same stored data for people; the snippets below are pretty-printed, abbreviated illustrations, not the entire schema.

## Successful JSON request

For the [quick start](getting-started.md), a request containing `{"name":"Ada","token":"example-secret"}` produces a record shaped like this:

```json
{
  "version": 1,
  "id": "7b7e97ad1338405faa9b0537e5780e2a",
  "time": "2026-10-06T16:00:00Z",
  "duration_ns": 12400000,
  "method": "POST",
  "url": "/hello",
  "path": "/hello",
  "route": "POST /hello",
  "protocol": "HTTP/1.1",
  "client_ip": "127.0.0.1",
  "status": 200,
  "request_id": "example-request-001",
  "request_headers": {
    "Content-Type": ["application/json"],
    "Authorization": ["***FILTERED***"]
  },
  "request": {
    "state": "complete",
    "content_type": "application/json",
    "data": {"name": "Ada", "token": "***FILTERED***"}
  },
  "response": {
    "state": "complete",
    "content_type": "application/json",
    "data": {"message": "Hello, Ada"}
  }
}
```

IDs, timestamps, durations and headers vary per request. `id` identifies this event. `request_id` links the request to other application logs; it can be accepted/generated when correlation is enabled. `trace_id` is separate and may be absent. The quick start explicitly copies its request ID into the response header; the logger does not inject that header automatically.

Header values are arrays. JSON bodies remain JSON objects/arrays/values, not double-encoded strings. Additional body fields include `bytes` (bytes observed by the adapter) and `encoding` when set. Limits, unread streams and omissions mean these byte counts need not equal a full wire payload or `Content-Length`.

`duration_ns: 12400000` means 12.4 ms. Durations in event/profile JSON are integer nanoseconds, not milliseconds. The dashboard renders human-readable durations. API status describes the observed response; it is independent of whether a background log write succeeds.

## Framework route differences

| Handler | Typical stored route |
| --- | --- |
| `http.ServeMux` registered as `POST /hello` | `POST /hello` |
| Gin `POST /users/:id` | `/users/:id` |
| chi `POST /users/{id}` | `/users/{id}` |
| Echo `POST /users/:id` | `/users/:id` |

Use the template in metrics and route policies. The `path` field contains the concrete request path. `name`, `group`, and `handler` are optional metadata; support depends on the router and metadata adapter. See [framework setup](http-integration.md).

## Errors and unread bodies

A JSON error response is captured in the same shape:

```json
{
  "method": "GET",
  "path": "/items/999",
  "status": 404,
  "request": {"state": "empty", "bytes": 0},
  "response": {
    "state": "complete",
    "data": {"error": "item not found"}
  }
}
```

An application error response does not imply a Go panic. `panicked` records an unrecovered panic observed by capture. If framework recovery runs inside the capture wrapper, capture sees its final response and may not see the panic itself. An unhandled panic before any response can have status `0`; capture does not invent a 500 response.

If a handler never consumes an incoming JSON request body, the record can contain:

```json
{"request": {"state": "unread", "bytes": 0, "content_type": "application/json"}}
```

Missing body data is intentional. The library observes reads/writes without draining the application stream to obtain a log.

## Capture states

`state` is a string describing capture, not an HTTP status. The net/http adapter and sanitizer use these values:

| State | Meaning |
| --- | --- |
| `complete` | A complete supported payload was observed and sanitized. |
| `empty` | No body bytes were present/observed at completion. |
| `unread` | The application did not read an expected request body. |
| `incomplete` | Only part of a payload was observed, or response handling did not complete. |
| `oversized` | The configured capture limit was exceeded. |
| `unsupported` | The content type is not selected or cannot be safely sanitized. |
| `invalid` | Complete captured bytes are malformed JSON or the sanitizer failed. |
| `omitted` | Capture policy, metadata-only/metrics-only behavior, or another restriction removed the body. |
| `streaming` | Server-sent events or explicit response flushing switched to metadata-only body capture. |
| `upgraded` | The response writer successfully handed the connection to the application through hijacking. |
| `encoded` | Compressed/encoded bytes are omitted by the adapter. |
| `file` | A Content-Disposition response is treated as a file. |
| `header_only` | A HEAD response has no captured body. |

See [privacy and custom sanitizers](configuration.md). Malformed or partial JSON does not fall back to storing raw text. [The Event/Body definitions](../types.go) and [sanitizer](../sanitize.go) are the schema/behavior reference for additional states.

## Optional SQL profile

With profiling enabled and a request-context-aware instrumented database path, the event also includes a summary:

```json
{
  "profile": {
    "instrumented": true,
    "incomplete": false,
    "query_count": 3,
    "duplicate_queries": 1,
    "sql_duration_ns": 18000000,
    "sql_wall_time_ns": 11000000,
    "handler_duration_ns": 15000000,
    "logger_overhead_ns": 350000,
    "stages": {"validation": 250000}
  }
}
```

This illustrates concurrent SQL work: cumulative `sql_duration_ns` can exceed both elapsed SQL wall time and handler duration. Do not subtract cumulative SQL duration from request duration. Profiles contain aggregates, never SQL argument values, raw statement text or result rows. Missing instrumentation and unfinished work have explicit flags/diagnostics. See [profiling](profiling.md).

## What the dashboard shows

The list shows timestamp, method, path, status, duration, and SQL count. A detail page displays formatted headers and payloads, capture-state markers, correlation and profile information. Search, date/status/method filters, charts and CSV exports operate on persisted logs. Application authorization decides who can view, export, or delete them. The [runnable standard example](../examples/README.md) demonstrates the complete flow.

The dashboard cannot show events dropped before persistence. Read `logger.Health().Outputs[outputName]`: `Accepted`, `Delivered`, `Dropped`, `Failed`, `Queued`, and `InFlight` describe delivery separately from API status. [Operations](operations.md) explains those counters and shutdown.
