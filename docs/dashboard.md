# Embedded request dashboard

The `dashboard` package mounts a request inspector inside your Go application. It uses your application's authentication and explicit permissions for viewing, exporting, and deleting logs. It does not start another server, create user accounts, or migrate a database.

The dashboard and all its CSS, JavaScript, and SVG charts are embedded in the root module. There are no external fonts, scripts, CDNs, frontend build steps, or JavaScript package dependencies.

## Mount the handler

Pass an initialized `apilog.Store` and a required authorization callback. The store schema must already exist. Authentication middleware must run before this handler so the callback can inspect your application's principal in `r.Context()`.

```go
package app

import (
    "net/http"
    "time"

    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/dashboard"
)

// authorize must verify an authenticated application principal and its permission
// for the supplied action. Never use an unconditional true callback in production.
func LogDashboard(store apilog.Store, authorize func(*http.Request, dashboard.Action) bool) (http.Handler, error) {
    return dashboard.New(store, dashboard.Options{
        BasePath:      "/ops/api-logs",
        Timezone:      time.UTC,
        SlowThreshold: 200 * time.Millisecond,
        Authorize:     authorize,
    })
}
```

Mount the returned handler at `/ops/api-logs/` in your existing router, behind the application's authentication middleware. `BasePath` must match the full externally visible path passed to the handler; do not strip that prefix. With `net/http`, use `mux.Handle("/ops/api-logs/", handler)`. Framework routers can mount this same `http.Handler` using their standard handler bridge.

Exclude the dashboard mount from API capture to avoid logging requests made while browsing the logs. Keep the dashboard on an internal or otherwise appropriately restricted application route. The handler never writes its own access logs.

### Options and defaults

| Option | Default | Behavior |
| --- | --- | --- |
| `Authorize` | Required | `func(*http.Request, Action) bool`; `false` returns 403. |
| `BasePath` | `/api-logs` | Clean absolute mount prefix; `/` is supported for a dedicated router. |
| `Timezone` | UTC | Display timezone and interpretation of date filters. |
| `SlowThreshold` | 200 ms | Threshold for slow/fast filtering. Set this to the same value used in logger configuration. |

The actions are `dashboard.View`, `dashboard.Export`, and `dashboard.Delete`. All endpoints, including assets and charts, require `View`. Export and deletion additionally require their own action permission. A missing authorizer is a construction error. The callback is invoked for every request and to decide which actions to show; it must be safe for concurrent use.

## Find and inspect requests

Requests are newest first by default. Sort by time, method, status, duration, or SQL query count. Pages contain 50 requests by default, with 25 and 100 available. Search matches the stored URL, request and response headers, and request and response JSON bodies.

The filter bar supports method, status codes, an inclusive local date range, slow/fast requests, and SQL buckets: 0-4, 5-9, 10 or more, and not tracked. SQL instrumentation that observed no queries is distinguishable from SQL instrumentation that was not enabled.

The detail view displays escaped request and response JSON, headers, correlation IDs, route metadata, body omission state, profiling durations, explicit stages, diagnostics, and detect-only security observations. Retained logs cannot be edited or created through the dashboard. Content displayed here has already passed through the logger's redaction and retention policies.

Opening **Traffic patterns** loads three charts with the current filters:

- Requests per UTC day.
- Request counts by status code.
- Average SQL query count per UTC day, over instrumented requests only. Days without instrumented requests are omitted from this chart.

Chart buckets always use UTC, even when timestamps and date filters use another display timezone. Charts load only when opened. Accessible data tables accompany each SVG chart, and a failed chart can be retried without reloading the request list.

## Export and delete

Select up to 100 requests on the current page, then use **Export selected** or **Delete selected**. An individual request can also be exported or deleted from its detail page. Selection uses explicit event IDs, so an action never silently applies to all matching rows.

CSV export includes request and response content, headers, correlation metadata, and profiling. All selected rows are validated before the response begins. If a selected request is missing or has `Event.ExportDisabled` set by the logging policy, the entire export is rejected. Potential spreadsheet formulas are prefixed with an apostrophe. Treat downloaded CSV files with the same access controls as stored logs.

Deletion is permanent. Both actions use POST and Go's `http.CrossOriginProtection` to reject cross-origin browser submissions using Fetch Metadata or Origin/Host validation. Modern browsers supply these headers automatically. Non-browser clients without either header are allowed by the standard library protection, but still require your application's authentication and action authorization. Reverse proxies must preserve the public Host header. No trusted-origin or CSRF-bypass option is exposed.

The dashboard sends `Cache-Control: no-store`, a restrictive Content Security Policy, and frame restrictions. Application authentication cookies and sessions remain the application's responsibility. Storage errors return a generic message without database errors, SQL statements, or credentials.

## Handler endpoints

All paths below are relative to `BasePath`. These are dashboard endpoints, not an additional application-wide management API.

| Method | Path | Permission | Purpose |
| --- | --- | --- | --- |
| GET | `/` | View | Filtered, paginated request list. |
| GET | `/events/{id}` | View | Read-only request detail. |
| GET | `/charts/requests` | View | Daily request counts. |
| GET | `/charts/status` | View | Status distribution. |
| GET | `/charts/sql` | View | Average SQL query counts. |
| POST | `/export` | View and Export | CSV for repeated form field `id`. |
| POST | `/delete` | View and Delete | Delete repeated form field `id`. |

List and chart filters share `q`, `method`, `status`, `after`, `before`, `speed`, and `sql`. Dates use `YYYY-MM-DD`; status codes accept comma-separated values. `speed` accepts `slow` or `fast`; `sql` accepts `low`, `medium`, `high`, or `unprofiled`. List controls add `sort`, `direction` (`asc` or `desc`), `page`, and `limit` (25, 50, or 100). Invalid values return 400.

Custom stores implement the root `apilog.Store` contract. The dashboard uses `List`, `Get`, `Aggregate`, and `Delete`; construction does not call `Migrate`, `Check`, or any write method. Retention and schema management remain explicit application or operations tasks.

## Verify

```sh
go test ./dashboard
go test -race ./dashboard
```

The tests cover application authorization, cross-origin rejection, persisted export restrictions, CSV formula handling, HTML escaping, timezone filters, shared chart filters, SQL instrumentation coverage, pagination, selection bounds, mount prefixes, and safe storage errors.
