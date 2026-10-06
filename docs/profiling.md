# SQL profiling and latency interpretation

Profiling is opt-in with `config.Profile.Enabled = true`. Keep request context attached to database calls. The HTTP middleware cannot discover arbitrary database activity without an installed SQL integration. The log database is independent of the application's database and must not be instrumented by this logger.

```sh
go get github.com/vishalanandl177/go-api-logger@v1.0.1
go get github.com/vishalanandl177/go-api-logger/integrations@v1.0.1
```

There are two separate setup steps: enable request profiling on the logger, then instrument the application's database pool or ORM. Keep your normal outputs and HTTP adapter in place:

```go
package app

import apilog "github.com/vishalanandl177/go-api-logger"

func ProfiledLogger(outputs []apilog.Output) (*apilog.Logger, error) {
    cfg := apilog.DefaultConfig()
    cfg.Outputs = outputs
    cfg.Profile.Enabled = true
    cfg.Profile.SampleRate = 1 // profile every request; use 0.1 for roughly 10%
    cfg.Profile.MaxQueries = 1000
    return apilog.New(cfg)
}
```

Create this logger once at startup. Sampling decides whether each request receives a profile; it does not sample individual queries within a selected request. A zero sample rate captures no profiles, even when `Enabled` is true.

| Application database path | Install once at startup | Request context |
| --- | --- | --- |
| `database/sql` | `apisql.OpenDB(connector)` or `apisql.WrapDriver(driver)` | `ExecContext`, `QueryContext`, `QueryRowContext`, `PrepareContext`, `BeginTx` |
| Native pgx / pgxpool | `apipgx.Install(config.ConnConfig)` for a pool | Pass `r.Context()` to pgx operations |
| GORM | `db.Use(apigorm.Plugin{})` | `db.WithContext(r.Context())` |
| Logger's own log store | No application profiling wrapper | Storage suppresses its own queries |

Choose the row matching the API your application actually calls. Do not add the GORM plugin and a separate `WrapLogger` to the same ORM instance. Built-in GORM and `database/sql` integrations suppress nested observations when their underlying driver is also instrumented, but one intended profiling source per database path keeps timing interpretation clear.

The root collector stores bounded normalized SQL fingerprints, counts and timings. It never persists SQL text, bound arguments, DSNs, returned rows or raw database errors. `MaxQueries` bounds retained fingerprints and time intervals; exceeding that bound marks the profile incomplete. SQL hooks run synchronously, while log storage runs through the existing bounded output queue.

## database/sql

Use `integrations/sql.OpenDB(connector)` instead of `sql.OpenDB(connector)`, or register `WrapDriver(driver)` under an application-chosen driver name and open that name. The returned object is a normal caller-owned `*sql.DB`; pooling, cancellation, prepared statements and transactions remain managed by `database/sql`.

```go
package example

import (
    "context"
    "database/sql"
    "database/sql/driver"

    apisql "github.com/vishalanandl177/go-api-logger/integrations/sql"
)

func OpenApplicationDB(connector driver.Connector) *sql.DB {
    return apisql.OpenDB(connector)
}

func UserIDs(ctx context.Context, pool *sql.DB, teamID int64) ([]int64, error) {
    rows, err := pool.QueryContext(ctx, "SELECT id FROM users WHERE team_id = ?", teamID)
    if err != nil { return nil, err }
    defer rows.Close()
    var ids []int64
    for rows.Next() {
        var id int64
        if err := rows.Scan(&id); err != nil { return nil, err }
        ids = append(ids, id)
    }
    return ids, rows.Err()
}
```

For a concrete connector, the pure-Go SQLite driver can be wired as follows. This creates an application pool, not the log-storage pool; initialize your application's tables using its own migration process. Install `modernc.org/sqlite@v1.60.1` if it is not already a direct dependency.

```go
package app

import (
    "context"
    "database/sql"

    apisql "github.com/vishalanandl177/go-api-logger/integrations/sql"
    "modernc.org/sqlite"
)

func OpenApplicationSQLite(ctx context.Context, filename string) (*sql.DB, error) {
    connector, err := sqlite.NewConnector(filename)
    if err != nil { return nil, err }
    db := apisql.OpenDB(connector)
    db.SetMaxOpenConns(1)
    db.SetMaxIdleConns(1)
    if err := db.PingContext(ctx); err != nil {
        _ = db.Close()
        return nil, err
    }
    return db, nil
}
```

Open the pool once at startup, pass `r.Context()` to `UserIDs` from the HTTP handler, and close the pool after the server drains. The example uses SQLite/MySQL placeholders; PostgreSQL uses `$1` instead of `?`. Driver wrappers preserve supported context operations, legacy execution fallback, named-value conversion, result-set metadata, session reset and validation. `ErrSkip` probes are not counted. Connection-specific functionality inside `sql.Conn.Raw` must unwrap the returned `driver.Conn` with `apisql.UnwrapConn` before asserting its concrete type.

Query timing includes dispatch and the lifetime of returned rows through exhaustion or close. This includes time the application spends consuming rows. Execute timing ends when the driver returns. Driver pool-wait time is not included. Transaction begin/commit/rollback and preparation are not counted as queries. Driver retries are observed attempts, not guaranteed distinct application operations. Use context-bearing APIs to retain request attribution; context-free operations cannot be attributed to a request.

Wrapped drivers suppress nested profiling when calling native drivers such as pgx, preventing duplicate capture by these integrations. Other OpenTelemetry or application instrumentation remains untouched.

## GORM

Install `integrations/gorm.Plugin{}` with `db.Use` and propagate context using `db.WithContext(r.Context())` or GORM's generic API. The plugin wraps the existing logger, preserves its callbacks and suppresses nested driver profiling. It counts ORM-observed SQL statements and excludes GORM dry runs. Parameter filtering prevents GORM from interpolating bound values for logging.

```go
package example

import (
    "context"

    apigorm "github.com/vishalanandl177/go-api-logger/integrations/gorm"
    "gorm.io/gorm"
)

type User struct { ID, TeamID int64 }

func ConfigureGORM(db *gorm.DB) error {
    return db.Use(apigorm.Plugin{})
}

func UsersForTeam(ctx context.Context, db *gorm.DB, teamID int64) ([]User, error) {
    var users []User
    err := db.WithContext(ctx).Where("team_id = ?", teamID).Find(&users).Error
    return users, err
}
```

Call `ConfigureGORM` once when opening the database, then pass `r.Context()` to `UsersForTeam` in the handler. GORM operation durations can include ORM work and preloading. Streaming `Row`/`Rows` operations finish their GORM observation before subsequent application scanning, unlike the driver-level rows lifetime. Use one primary profiling level when comparing timings. `WrapLogger(existing)` is available without the plugin, but must be used as an alternative to driver profiling for that pool; only the plugin provides nested-call suppression and dry-run awareness.

Keep your existing `gorm.Open(dialector, config)` and driver selection. Handle the error returned by `db.Use(apigorm.Plugin{})`; installing the plugin after individual queries have already run cannot recover their timings. Close GORM's underlying `*sql.DB` as part of normal application shutdown after request handlers have finished.

## Native pgx

Call `integrations/pgx.Install(config)` on `*pgx.ConnConfig` before connecting. For a pool, pass `poolConfig.ConnConfig`. Installation composes with existing query, batch, COPY, preparation, connection and pool tracers, and repeated installation is harmless.

```go
package example

import (
    "context"

    "github.com/jackc/pgx/v5/pgxpool"
    apipgx "github.com/vishalanandl177/go-api-logger/integrations/pgx"
)

func OpenPostgres(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
    config, err := pgxpool.ParseConfig(dsn)
    if err != nil { return nil, err }
    apipgx.Install(config.ConnConfig)
    pool, err := pgxpool.NewWithConfig(ctx, config)
    if err != nil { return nil, err }
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return nil, err
    }
    return pool, nil
}
```

Open the pool once at startup with a deadline-bound context and handle the returned error. This example calls `Ping` because pgx pool construction alone is lazy. Pass each request's context to pool operations and call `pool.Close()` after HTTP requests drain. Normal queries end when result rows close. Batch counts are reported as result callbacks arrive, and each result's measured lifetime starts at `SendBatch`; pipelined lifetimes overlap. `sql.batch` measures the batch lifetime, and `sql.pool.acquire` measures pool acquisition through native pgx hooks. COPY records one operation, without reading copied values. Aborted or abandoned batches may report fewer observed result callbacks than submitted statements. Close result sets and batch results promptly.

## Context inside an HTTP handler

The capture middleware creates the request-local collector before it invokes your handler. Keep `r.Context()` on database calls; replacing it with `context.Background()` loses attribution. The following handler works with an already instrumented `*sql.DB` and an existing application table:

```go
package app

import (
    "database/sql"
    "encoding/json"
    "net/http"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func UserCount(db *sql.DB) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        apilog.MarkInstrumented(r.Context()) // db is known to use the SQL wrapper
        var count int64
        // A fixed stage name groups your application work; it is not a SQL query.
        endStage := apilog.StartStage(r.Context(), "load_summary")
        err := db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users").Scan(&count)
        endStage()
        if err != nil {
            http.Error(w, "cannot load summary", http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]int64{"users": count})
    })
}
```

Mount the handler inside `httpmw.Middleware(logger)`. In Gin, pass `c.Request.Context()`; in Echo, pass `c.Request().Context()`; chi handlers use `r.Context()`. Goroutines that inherit the context but finish after the HTTP handler returns cannot change an already emitted profile. Finish request-scoped database work before returning, or instrument detached work separately in your application's own telemetry.

## Request summaries

- `Instrumented=false` means no instrumentation has confirmed coverage for the request. It does not mean zero SQL queries.
- If a request is known to use an instrumented database even when it executes no SQL, call `apilog.MarkInstrumented(r.Context())` in the application middleware that supplies that database. An instrumented zero-query request can then be distinguished from missing coverage.
- `QueryCount` is the number of observed query operations; `DuplicateQueries` counts repeated normalized shapes. Repeated shapes are possible N+1 hints, not proof of an ORM relationship problem.
- `SQLDuration` sums observed lifetimes and can exceed request duration when queries overlap. It is not database engine execution time.
- `SQLWallTime` is the union of completed observed intervals inside the request window. Subtracting cumulative SQL duration from request duration is invalid. Neither wall-time remainder nor custom stages should be presented as measured CPU time.
- `HandlerDuration` is elapsed time inside the outer capture middleware, including downstream middleware and capture callbacks. `LoggerOverhead` records capture callbacks plus event preparation through the profiling snapshot. It excludes subsequent observer/output serialization, queue admission and asynchronous writes. Optional logger timing metrics cover the full emission path; these values are not a subtraction-based estimate of application CPU time.
- Unclosed observed queries, unfinished stages or batches, and collection limits mark summaries incomplete. The request event finishes when the handler returns and does not wait for background work. Late completion cannot mutate an emitted event.

Use `StartStage(ctx, "remote_api")` to time an application-defined operation and call its returned function once. Stage names should be fixed strings rather than user input. Sampling applies at the request level. Keep instrumentation disabled or sampled for hot paths if measured overhead exceeds your application's budget.

If the dashboard says **Not tracked**, verify both profiling configuration and database instrumentation, then verify that calls use the request context. `MarkInstrumented` declares known coverage; it does not wrap a database or discover SQL by itself. An empty profile does not prove the handler avoided database work.

## Verification

The integration tests run SQLite in process through the pure-Go driver and verify prepared queries, transactions, legacy fallback, custom named-value conversion, cancellation and rows closed after request completion. GORM tests execute real SQLite queries with an instrumented underlying driver and assert that operations are not counted twice.

Native pgx callback and composition tests always run. Set `APILOG_TEST_POSTGRES_DSN` to an isolated PostgreSQL test instance to run the additional live query, batch and COPY test. It creates a connection-local temporary table. Tests never print the DSN. No database credentials belong in committed configuration.
