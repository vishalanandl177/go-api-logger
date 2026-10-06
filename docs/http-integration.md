# Integrate with an HTTP server

Go API Logger captures a request by wrapping an `http.Handler`. A standard mux, Gin engine, chi router, Echo instance, or your own `ServeHTTP` implementation can all use the same outer wrapper. Framework helpers add route metadata; they do not create another log event.

Start with the [complete quick start](../README.md#quick-start) for logger construction and shutdown, then use the matching recipe below. The module requires Go 1.26 or newer; the release is tested on Go 1.26 and 1.27.

## Choose the integration

| Application | Capture | Route metadata | Runnable example |
| --- | --- | --- | --- |
| `net/http`, `http.ServeMux` | `httpmw.Middleware(logger)(mux)` | ServeMux `Request.Pattern` is recognized automatically | [net/http](../integrations/examples/nethttp/main.go) |
| `http.Handler` or `http.HandlerFunc` | Same wrapper | Call `apilog.SetRoute` if your router does not set `Request.Pattern` | [generic handler recipe](#an-existing-handler-or-server) |
| Gin v1 | Wrap the engine | `apigin.Metadata("users")` | [Gin](../integrations/examples/gin/main.go) |
| chi v5 | Wrap the router | `apichi.Metadata("users")` | [chi](../integrations/examples/chi/main.go) |
| Echo v4 | Wrap the Echo instance | `apiecho.Metadata("users")` | [Echo v4](../integrations/examples/echov4/main.go) |
| Echo v5 | Wrap the Echo instance | `apiecho.Metadata("users")` from `echov5` | [Echo v5](../integrations/examples/echov5/main.go) |
| Native Fiber or fasthttp | No native adapter in this release | Not covered by the HTTP conformance claim | [custom server boundary](#servers-without-nethttp) |

Install the core in your application module:

```sh
go get github.com/vishalanandl177/go-api-logger@v1.0.1
```

For Gin, chi, or Echo, also install the separate integration module:

```sh
go get github.com/vishalanandl177/go-api-logger/integrations@v1.0.1
```

These are module commands. Import the adapter package shown below; do not try to install a nonexistent `integrations/gin` module tag. The standard HTTP wrapper needs only the core module.

## An existing handler or server

This is a complete helper file you can add to an application that already constructs a logger and router:

```go
package logging

import (
    "net/http"

    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
)

func Wrap(logger *apilog.Logger, handler http.Handler) http.Handler {
    return httpmw.Middleware(logger)(handler)
}

func WrapFunc(logger *apilog.Logger,
    handler func(http.ResponseWriter, *http.Request)) http.Handler {
    return httpmw.Middleware(logger)(http.HandlerFunc(handler))
}

// Call once, before server.ListenAndServe or server.Serve.
func Attach(logger *apilog.Logger, server *http.Server) {
    handler := server.Handler
    if handler == nil {
        handler = http.DefaultServeMux
    }
    server.Handler = Wrap(logger, handler)
}
```

Use one `apilog.Logger` for the application lifetime. Configure at least one output for logs, or an observer for metrics-only use. Do not construct a logger for each request or wrap the same handler twice. Do not replace the server's handler after it has started.

For a custom router, set a stable route template before reading a body or writing a response:

```go
apilog.SetRoute(r.Context(), "/orders/{id}", "orders.show", "orders")
apilog.SetHandler(r.Context(), "ShowOrder")
```

Here `r` is the request passed to the wrapped handler. Use `/orders/{id}` as the route and keep the concrete path `/orders/42` in the event's path field. Preserve `r.Context()` when deriving requests or calling application services. For routers that discover their route later, `apilog.SetRouteResolver` is the advanced helper used by the chi adapter; its returned finalizer must run before pooled router context is reused. See its [source contract](../context.go).

## Gin v1

Install the versions verified by this release if Gin is not already in your module:

```sh
go get github.com/gin-gonic/gin@v1.12.0
```

This helper returns the final handler for your existing `http.Server`:

```go
package logging

import (
    "net/http"

    "github.com/gin-gonic/gin"
    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
    apigin "github.com/vishalanandl177/go-api-logger/integrations/gin"
)

func GinHandler(logger *apilog.Logger) http.Handler {
    router := gin.New()
    router.Use(apigin.Metadata("users"), gin.Recovery())
    router.GET("/users/:id", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{"id": c.Param("id")})
    })
    return httpmw.Middleware(logger)(router)
}
```

Assign the result to `server.Handler`; start that server instead of calling `router.Run()`, which would bypass this outer handler. For an existing `gin.Default()` engine, retain its recovery middleware and add `apigin.Metadata` before registering routes. Route metadata is `/users/:id`; Gin's Go handler name is stored separately. Application context is `c.Request.Context()`.

## chi v5

```sh
go get github.com/go-chi/chi/v5@v5.3.2
```

```go
package logging

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
    apichi "github.com/vishalanandl177/go-api-logger/integrations/chi"
)

func ChiHandler(logger *apilog.Logger) http.Handler {
    router := chi.NewRouter()
    router.Use(apichi.Metadata("users"), middleware.Recoverer)
    router.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]string{"id": chi.URLParam(r, "id")})
    })
    return httpmw.Middleware(logger)(router)
}
```

Install metadata with `Use` before registering routes. It resolves chi's route template when capture decisions need it and snapshots it before the framework context can be reused. Application context is `r.Context()`.

## Echo v4 and v5

Choose the import matching your application. Do not mix major versions:

```sh
# Echo v4 application
go get github.com/labstack/echo/v4@v4.16.0
# OR Echo v5 application
go get github.com/labstack/echo/v5@v5.4.0
```

Echo v4 helper:

```go
package logging

import (
    "net/http"

    "github.com/labstack/echo/v4"
    "github.com/labstack/echo/v4/middleware"
    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
    apiecho "github.com/vishalanandl177/go-api-logger/integrations/echov4"
)

func EchoV4Handler(logger *apilog.Logger) http.Handler {
    router := echo.New()
    router.Use(apiecho.Metadata("users"), middleware.Recover())
    router.GET("/users/:id", func(c echo.Context) error {
        return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
    })
    router.GET("/error", func(c echo.Context) error {
        return echo.NewHTTPError(http.StatusUnprocessableEntity, "example validation error")
    })
    return httpmw.Middleware(logger)(router)
}
```

Echo v5 uses a pointer context and the v5 adapter:

```go
package logging

import (
    "net/http"

    "github.com/labstack/echo/v5"
    "github.com/labstack/echo/v5/middleware"
    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/httpmw"
    apiecho "github.com/vishalanandl177/go-api-logger/integrations/echov5"
)

func EchoV5Handler(logger *apilog.Logger) http.Handler {
    router := echo.New()
    router.Use(apiecho.Metadata("users"), middleware.Recover())
    router.GET("/users/:id", func(c *echo.Context) error {
        return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
    })
    router.GET("/error", func(c *echo.Context) error {
        return echo.NewHTTPError(http.StatusUnprocessableEntity, "example validation error")
    })
    return httpmw.Middleware(logger)(router)
}
```

Assign the returned handler to an application-owned `http.Server`. Starting Echo directly would bypass this wrapper. Capture stays outside Echo so its central error handler can render the final response before the event is finished. Both adapters store the route template; v5 also reads the registered route name. Application context is `c.Request().Context()`.

## Middleware order and response behavior

Use this request order:

```text
existing tracing / request-local Sentry hub
  -> httpmw.Middleware(logger)
    -> framework, route metadata, recovery, authentication, handlers
      -> framework final error rendering
```

The handler stack returns in the opposite order. Authentication and recovery inside capture make their returned statuses visible. Place tracing outside capture so its span remains active during final observer enrichment. See [OpenTelemetry and Sentry](integrations.md#opentelemetry).

The capture middleware does not convert a panic into an HTTP 500. If framework recovery handles it, the final response is logged, but `Panicked` is false because capture never observed an escaping panic. If a panic escapes, capture records `Panicked: true` and re-panics with the original value. Its status is 0 if nothing was committed, or the already committed status after a partial response. Do not infer that every HTTP 500 is an exception.

Recoverable panics from logging extension hooks are isolated from the handler. A failed request-ID generator falls back to a generated ID; a failed custom sanitizer omits the affected body; a failed transform drops that event; a failed route resolver suppresses the exchange. A failed policy restricts the event to metadata without headers, bodies, query parameters, or export delivery. A capture-time policy failure keeps those restrictions for the whole exchange, clears security samples, and disables its security inspection even if the final policy call succeeds. Observer failures do not alter the response. Hooks are invoked again on later requests, so a transient hook failure does not permanently disable logging. This is separate from application panic recovery: panics from your handler or underlying request/response I/O still propagate to your server's recovery layer.

| Behavior | Capture contract |
| --- | --- |
| No explicit response status | Records 200 when the handler returns normally. |
| Redirects and central error handlers | Records final status and headers written inside capture. |
| Request body | Observes bytes as the application reads them; never pre-reads or drains for logging. |
| JSON response | Set `Content-Type: application/json` before writing. Supported `application/*+json` types are also captured. |
| Partial writes | Records only bytes accepted by the underlying writer and preserves write errors. |
| SSE or flushing | Forwards flushing and marks the response `streaming`; body data is omitted. The event finishes when the handler returns, not at each message. |
| Connection upgrade | Preserves supported hijacking; captures metadata with state `upgraded`, not subsequent socket traffic. |
| HTTP/2 | Preserves the underlying writer's capabilities. An interface absent on that writer stays absent. |
| `ResponseController` | Supports unwrapping to the original writer for deadlines and full-duplex operation; flushes still pass through capture. |
| Compression | A response with non-identity `Content-Encoding` is metadata-only (`encoded`). Logger does not decompress it. |
| Files and multipart | File responses and unsupported content types omit body data. The application still sends or receives the original bytes. |

The writer exposes `http.Flusher`, `http.Hijacker`, `http.Pusher`, `io.ReaderFrom`, `http.CloseNotifier`, and `FlushError` only when the underlying writer supports them. Other middleware must preserve the same capabilities if your application needs them. Changing compression order does not guarantee JSON capture: compression middleware may set `Content-Encoding` before inner capture writes. Test the assembled stack and expect metadata-only capture whenever that header is visible.

## Read bodies normally and check capture states

Default limits are 32 KiB for requests and 64 KiB for responses. They bound what the logger retains; they are not application request-size enforcement. Use your server's own body-size limits where needed.

For an application that does not read the request, `unread` is expected. When only part of a JSON body is consumed, the logger omits its data as `incomplete`. Oversized JSON becomes `oversized`, and malformed JSON becomes `invalid`. None of these cases falls back to unsafe raw text. See [privacy and policies](configuration.md) for masking and capture options.

Examples parse `POST /users` JSON in the application handler, then return `{"created":true}`. Use only fictional credentials in the demo:

```sh
curl -i http://127.0.0.1:8080/users/42
curl -i -H 'Content-Type: application/json' \
  -d '{"name":"Ada","token":"demo-token-not-a-real-credential"}' \
  http://127.0.0.1:8080/users
```

PowerShell equivalent:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/users/42
Invoke-RestMethod http://127.0.0.1:8080/users -Method Post `
  -ContentType 'application/json' `
  -Body '{"name":"Ada","token":"demo-token-not-a-real-credential"}'
```

Expect HTTP 201, `request.state: "complete"`, and `request.data.token: "***FILTERED***"` in stdout. The example runner sets `Queue.BatchSize = 1` so the first event appears promptly. Production defaults are a batch of 50 or a 10-second flush interval, so a single request does not normally appear immediately. `Flush(ctx)` is useful in tests and administrative actions; calling it after every request makes delivery part of the request path.

## Run and stop the examples

From a checkout of this repository:

```sh
cd integrations
go run ./examples/nethttp
# Or choose one:
go run ./examples/gin
go run ./examples/chi
go run ./examples/echov4
go run ./examples/echov5
```

Run one at a time; all bind `127.0.0.1:8080`. Each example shares the [lifecycle runner](../integrations/examples/internal/run/run.go): on interrupt or SIGTERM it stops HTTP admission, drains HTTP requests with a timeout, then calls `logger.Shutdown` with a fresh timeout. Keep output resources, including log database connections, open until logger shutdown completes. They are minimal integration demos, not authentication or persistence implementations. See the [SQLite dashboard application](../examples/README.md) for storage and a local dashboard.

## Servers without net/http

Native Fiber and fasthttp adapters are not shipped. An interoperability bridge may give you an `http.Handler`, but this release does not certify the bridge's streaming, cancellation, upgrade, or pooled-buffer ownership behavior.

The [core `Logger.Emit(ctx, Event)` API](../logger.go) can accept an owned event from a custom adapter. The adapter must supply accurate method, path, status, timestamps, duration, headers, body states, and limits. The core applies emission policies and sanitization before observers and outputs, but `Emit` alone does not perform capture-time policy checks, create a profiling context, or inspect security samples. A full adapter uses [`Logger.Begin`, `Exchange.CaptureAllowed`, and `Exchange.Finish`](../context.go), calls `Finish` once after final response rendering including panic paths, and owns the server-specific capture contract. Do not retain pooled framework buffers or context objects in an event. Pass the [HTTP conformance tests](../httpmw/conformance_test.go) and framework-specific equivalents before claiming compatibility.

## Verify an integration

Exercise a valid JSON request, unread and oversized requests, a framework error, a recovered panic, a 404, and your streaming or upgrade routes. Check that each exchange produces at most one event, a route template is present for matched routes, and fictional secrets are masked. Wait for asynchronous delivery before checking stored rows. Use `logger.Health()` to check failed or dropped output records under load; bounded queues can drop records even while the API succeeds.
