# SQL storage

The optional `github.com/vishalanandl177/go-api-logger/storage` module implements the same Store contract for PostgreSQL, MySQL and SQLite. It imports pgx's database/sql driver, go-sql-driver/mysql and the pure Go modernc SQLite driver. Core logging has no database driver dependency.

```sh
go get github.com/vishalanandl177/go-api-logger@v1.0.1
go get github.com/vishalanandl177/go-api-logger/storage@v1.0.1
```

## Open a store and attach it to the logger

The following startup helper handles all three databases. Load `dsn` from your deployment's secret/configuration provider, for example `os.Getenv("API_LOGGER_DSN")`; do not put database credentials in source code. Use `migrate=true` only for an explicit migration command or setup run. Normal server startup uses `migrate=false` and verifies the existing schema.

```go
package app

import (
    "context"
    "database/sql"
    "errors"

    apilog "github.com/vishalanandl177/go-api-logger"
    "github.com/vishalanandl177/go-api-logger/storage"
)

type LogResources struct {
    Logger *apilog.Logger
    Store  *storage.SQLStore
    DB     *sql.DB // caller-owned; close only after Logger.Shutdown succeeds
}

func OpenLogs(ctx context.Context, driver, dsn string, migrate bool) (*LogResources, error) {
    var store *storage.SQLStore
    var db *sql.DB
    var err error
    switch driver {
    case "postgres":
        store, db, err = storage.OpenPostgres(ctx, dsn)
    case "mysql":
        store, db, err = storage.OpenMySQL(ctx, dsn)
    case "sqlite":
        store, db, err = storage.OpenSQLite(ctx, dsn)
    default:
        return nil, errors.New("unsupported log database driver")
    }
    if err != nil { return nil, err }
    if migrate {
        if err := store.Migrate(ctx); err != nil {
            _ = db.Close()
            return nil, err
        }
    }
    if err := store.Check(ctx); err != nil {
        _ = db.Close()
        return nil, err
    }
    cfg := apilog.DefaultConfig()
    cfg.Outputs = []apilog.Output{{Name: "database", Kind: "storage", Sink: store}}
    logger, err := apilog.New(cfg)
    if err != nil {
        _ = db.Close()
        return nil, err
    }
    return &LogResources{Logger: logger, Store: store, DB: db}, nil
}
```

Use a deadline-bound startup context, such as `context.WithTimeout(context.Background(), 15*time.Second)`. The returned logger runs independently of that startup context. Pass `resources.Logger` to the HTTP adapter and `resources.Store` to the dashboard. Do not `defer db.Close()` inside `OpenLogs`: the background worker needs that database after this function returns. See [shutdown ordering](operations.md#delivery-and-shutdown) for the closing sequence.

| Driver | DSN shape | Setup |
| --- | --- | --- |
| `sqlite` | `/var/lib/myapp/api-logs.db` or `./api-logs.db` | Parent directory must exist and be writable. A new file is created when opened for writing. |
| `postgres` | pgx connection URL or keyword/value connection string | Provision the database and account first; configure TLS in the DSN. |
| `mysql` | go-sql-driver/mysql connection string (`user:password@tcp(host:3306)/database`) | Provision the database and account first; configure TLS using the driver. |

The MySQL shape above describes syntax, not credentials to copy. For PostgreSQL and MySQL, migrations create the logging tables inside an existing database; they do not provision the server, database, account, or network access. No separate blank driver import is needed when using `storage.Open*`.

`OpenPostgres(ctx, dsn)` and `OpenMySQL(ctx, dsn)` return the same `(store, db, error)` shape. The Open helpers check connectivity but do not run migrations. Applications already owning a pool can call `NewPostgres(db)`, `NewMySQL(db)`, `NewSQLite(db)`, or `New(db, dialect)`. Stores never close or reconfigure caller-owned pools. OpenSQLite sets its newly created pool to one connection to support `:memory:` and serialize SQLite writes. Use a dedicated log database where possible.

For combined database and subscriber delivery, append an `apilog.Output{Name: "json", Kind: "export", Sink: &apilog.JSONSink{Writer: os.Stdout}}` to `cfg.Outputs` before `apilog.New`. Each output gets its own queue. A policy can disable export while keeping storage, or disable storage while keeping export. See [output examples](configuration.md#custom-outputs).

`OpenSQLiteReadOnly(ctx, dsn)` requires an existing file and forces SQLite's `mode=ro`. It accepts plain filenames and `file:` URIs, with optional `mode=ro|rw|rwc` and `cache=private|shared`; the mode is always replaced with `ro`. It rejects memory databases, duplicate parameters, PRAGMA/driver options and all other options before opening. Use a separate diagnostic DSN without `_pragma` or shorthand options. The returned store supports reads and dry-run pruning; writes fail at the database connection.

## Schema and writes

Call `Migrate(ctx)` explicitly during deployment. Schema version 1 creates `api_logger_schema` and `api_logger_events`, with indexed timestamp, method, status, duration and nullable SQL count. Complete sanitized events are stored as JSON text, with a separate lowercased search document. All payload copies are already sanitized, so retention removes both together. No credentials, raw query text or connection strings appear in storage error messages.

Migrations serialize through PostgreSQL advisory locking, MySQL named locking, or a SQLite write transaction. Initial DDL can be rerun safely after an interruption. A schema newer than this binary is rejected. Back up data before upgrades. Schema migrations are never run from request middleware. MySQL DDL is not transactional; deployment tooling should treat failed migrations as a readiness failure and rerun after correcting the cause.

`WriteBatch` executes one prepared insert per event in a transaction. A duplicate event ID or any failed insert rolls back the entire batch. There is no retry or durable spool: the logger accounts for failed batches and protects request latency according to its bounded delivery policy. Storage queries use the profiling-suppressed context so the logger's own queries do not appear in an application's request profile.

## Query semantics

- `Get` returns `apilog.ErrNotFound` for an absent ID.
- `List` defaults to page 1, 50 rows, newest first. The maximum page size is 500. Supported order keys are `time`, `duration`, `status`, `method`, and `sql`; set `Descending` when supplying an explicit order. IDs break ties deterministically. Lists are live reads, so totals may change between count and page queries while writes continue.
- `Search` is a literal, case-insensitive substring across sanitized URL, request/response headers and request/response bodies. `%`, `_`, quotes and SQL fragments are treated as data. Search does not inspect correlation context, SQL, or arbitrary profile attributes. It is portable substring search, not a full-text index; use selective date/status/method filters for large datasets.
- `After` is inclusive; `Before` is exclusive. Timestamp filters use UTC epoch microseconds. Full event timestamps retain their original JSON precision.
- `Slow` requires a positive `SlowThreshold`; slow includes the threshold and fast excludes it. SQL minimum and maximum are inclusive.
- `Profiled` means SQL instrumentation was available, not simply that HTTP timing was captured. Uninstrumented profiles store a null SQL count. Instrumented requests with no queries store zero.
- `IDs`, methods and statuses support matching sets. Empty sets apply no filter. Delete with an empty ID list deletes nothing. Individual ID filters/deletion accept at most 1000 IDs.
- `Aggregate` applies the same search and filters, ignores pagination and ordering, and returns daily call counts, status counts, and average SQL count for instrumented requests. `ProfiledCount` distinguishes a real zero-query average from a day without SQL instrumentation. Day buckets are UTC; timestamp display can use the dashboard's selected timezone.

`Delete` removes only explicitly provided IDs. `Prune` uses a fixed exclusive cutoff and batches of at most 1000 rows. `DryRun` counts without deleting. Cancellation or a storage failure returns the number already deleted. Concurrent writers may make the initial matched count differ from eventual deletion count. Retention is not a single long transaction.

This read-only helper uses the same filter for the log list and charts. It works with any implementation of `apilog.Store`:

```go
package app

import (
    "context"
    "time"

    apilog "github.com/vishalanandl177/go-api-logger"
)

func RecentFailures(ctx context.Context, store apilog.Store, since time.Time) (apilog.Page, apilog.Analytics, error) {
    filter := apilog.Filter{
        After: since, StatusCodes: []int{500, 502, 503, 504},
        Page: 1, Limit: 50, Order: "time", Descending: true,
    }
    page, err := store.List(ctx, filter)
    if err != nil { return apilog.Page{}, apilog.Analytics{}, err }
    charts, err := store.Aggregate(ctx, filter)
    return page, charts, err
}
```

Writes are asynchronous. To inspect events immediately after a test request, finish the handler, call `logger.Flush(ctx)`, and check `logger.Health()` for failed/dropped records before querying. `Flush` is not needed on every production request and should not be placed in request middleware.

## Verification

From `storage/`, run `go test ./...` for live SQLite contract tests. The same suite runs against PostgreSQL and MySQL when `API_LOGGER_TEST_POSTGRES_DSN` and `API_LOGGER_TEST_MYSQL_DSN` are set. These must point at dedicated empty test databases. The suite refuses existing rows before its write/delete/prune scenarios.

`storage/compose.yaml` provisions scratch PostgreSQL 17 and MySQL 8.4 services on loopback ports. On Windows, `storage/test-integration.ps1` starts them, waits for health, runs the suite and stops the services. Docker is required for this helper. On other hosts:

```sh
cd storage
docker compose up -d --wait
export API_LOGGER_TEST_POSTGRES_DSN='postgres://apilog:local-test-only@127.0.0.1:55432/apilog_test?sslmode=disable'
export API_LOGGER_TEST_MYSQL_DSN='apilog:local-test-only@tcp(127.0.0.1:53306)/apilog_test'
go test -race -count=1 ./...
docker compose down
```

The tests cover migrations, all query filters, exact boundary behavior, literal search/injection handling, UTC charts, pagination, atomic rollback, cancellation, concurrent access, dry-run retention and deletion. PostgreSQL/MySQL tests are skipped without their environment variables; a skipped test is not evidence that the corresponding backend was exercised.

Driver references: [pgx database/sql](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib), [MySQL driver](https://github.com/go-sql-driver/mysql), [modernc SQLite](https://modernc.org/sqlite).
