# Integrate your first API

Use Go 1.26 or 1.27. Start with JSON output so you can verify capture and masking before adding a database or dashboard. The core package is named `apilog`; its module path is `github.com/vishalanandl177/go-api-logger`.

## Start a new project

Run these commands in a new directory:

```sh
mkdir api-logger-demo
cd api-logger-demo
go mod init example.com/api-logger-demo
go get github.com/vishalanandl177/go-api-logger@v1.0.1
```

Save the complete application from the [README quick start](../README.md#quick-start) as `main.go`. The same source is maintained in [examples/quickstart/main.go](../examples/quickstart/main.go). It needs only the core module.

```sh
go run .
```

The application listens on `127.0.0.1:8080`. If that port is occupied, change `server.Addr` in the example. Keep this terminal open and send the following request from another terminal.

### Linux, macOS, or Git Bash

```sh
curl -i http://127.0.0.1:8080/hello \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer example-token' \
  -H 'X-Request-ID: example-request-001' \
  -d '{"name":"Ada","token":"example-secret"}'
```

### PowerShell

```powershell
Invoke-RestMethod 'http://127.0.0.1:8080/hello' -Method Post `
  -ContentType 'application/json' `
  -Headers @{ Authorization = 'Bearer example-token'; 'X-Request-ID' = 'example-request-001' } `
  -Body '{"name":"Ada","token":"example-secret"}'
```

These credentials are fictional sample values, not an authentication implementation.

## Check the result

The response is HTTP 200 with `{"message":"Hello, Ada"}`. The example also returns `X-Request-ID: example-request-001` because its handler explicitly sets that header.

Within roughly one second, the server terminal prints one JSON event. It includes `method: "POST"`, `route: "POST /hello"`, `status: 200`, and the same request ID. The captured `token` and `Authorization` value are `***FILTERED***`. The original handler still receives the original request; redaction changes the logged copy.

The example uses a one-second flush interval for visibility. The library default is ten seconds; a batch can be delivered sooner when it fills. See the [annotated log format](log-format.md) for the JSON structure and capture states.

Stop with Ctrl+C. The example drains HTTP requests before shutting down the logger. In a service, use the application's existing shutdown coordinator and do not close log database pools or output files until logger shutdown has finished.

## Add logging to an existing project

Do not replace your router or create a second listening server. Construct one logger during application startup, then wrap the handler already passed to your server:

```go
// logger is an *apilog.Logger constructed once at startup.
// existingRouter implements http.Handler.
server := &http.Server{
    Addr: ":8080",
    Handler: httpmw.Middleware(logger)(existingRouter),
    ReadHeaderTimeout: 5 * time.Second,
}
```

This fragment needs `net/http`, `time`, and `github.com/vishalanandl177/go-api-logger/httpmw`. It replaces the `Handler` assignment in your existing startup code, not your lifecycle management. You can also assign `server.Handler = httpmw.Middleware(logger)(server.Handler)` before starting a server with a non-nil handler. For a nil handler, explicitly use `http.DefaultServeMux`.

Follow [the HTTP integration guide](http-integration.md) for `http.HandlerFunc`, middleware ordering, and full Gin, chi, and Echo examples. Framework metadata middleware belongs inside the router; the capture wrapper belongs outside the framework's final error handling.

## Choose the next feature

| Goal | Next step |
| --- | --- |
| View what each field means | [Log format](log-format.md) |
| Use Gin, chi, Echo, or another `http.Handler` | [HTTP integration](http-integration.md) |
| Mask application fields, omit payloads, or customize output | [Configuration](configuration.md) |
| Save logs to PostgreSQL, MySQL, or SQLite | [Storage](storage.md) |
| Search logs in your application | [Dashboard and permissions](dashboard.md) |
| Count application SQL operations | [Profiling](profiling.md) |
| Add metrics or existing tracing | [Observability integrations](integrations.md) |
| Give a coding assistant an integration specification | [AI integration guide](ai-integration.md) |

## Troubleshooting the first request

| Symptom | Check |
| --- | --- |
| No output | Configure at least one `cfg.Outputs` entry before `apilog.New`; wait for the flush interval; inspect `logger.Health()` and `logger.Diagnose()`. |
| Request body says `unread` | The application never read the body. Logging does not consume it on the application's behalf. |
| Body says `unsupported` | Send `Content-Type: application/json`; other formats need an explicit sanitizer and configuration. |
| JSON body omitted | Inspect the capture state. Oversized, malformed, incomplete, streaming, and encoded payloads are not a raw fallback path. |
| Gin or Echo route is missing | Install the framework `Metadata` middleware and the outer HTTP capture wrapper. |
| Two log records for one request | Remove a duplicate capture wrapper; metadata enrichers alone do not emit events. |
| Final Echo error body is missing | Wrap the Echo engine in `httpmw.Middleware`; an inner Echo middleware can finish before centralized error rendering. |
| SQL count is zero | Enable profiling, instrument one database path, and pass the request context to query calls. |
| Successful requests but missing records | Read the `Dropped` and `Failed` counters. Delivery is asynchronous and best effort. |

For runnable storage, dashboard, metrics, and profiling together, use the [standard example](../examples/README.md). Native Fiber/fasthttp adapters are not included in this release.
