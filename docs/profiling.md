# SQL profiling and latency interpretation

Profiling is opt-in with `config.Profile.Enabled = true`. Keep request context attached to database calls. The HTTP middleware cannot discover arbitrary database activity without an installed SQL integration. The log database is independent of the application's database and must not be instrumented by this logger.

The root collector stores bounded normalized SQL fingerprints, counts and timings. It never persists SQL text, bound arguments, DSNs, returned rows or raw database errors. `MaxQueries` bounds retained fingerprints and time intervals; exceeding that bound marks the profile incomplete. SQL hooks run synchronously, while log storage runs through the existing bounded output queue.

## database/sql

Use `integrations/sql.OpenDB(connector)` instead of `sql.OpenDB(connector)`, or register `WrapDriver(driver)` under an application-chosen driver name and open that name. The returned object is a normal caller-owned `*sql.DB`; pooling, cancellation, prepared statements and transactions remain managed by `database/sql`.

```go
pool := apisql.OpenDB(connector)
defer pool.Close()
// In the HTTP handler:
rows, err := pool.QueryContext(r.Context(), "SELECT id FROM users WHERE team_id = ?", teamID)
if err != nil { /* handle application error */ }
defer rows.Close()
```

Driver wrappers preserve supported context operations, legacy execution fallback, named-value conversion, result-set metadata, session reset and validation. `ErrSkip` probes are not counted. Connection-specific functionality inside `sql.Conn.Raw` must unwrap the returned `driver.Conn` with `apisql.UnwrapConn` before asserting its concrete type.

Query timing includes dispatch and the lifetime of returned rows through exhaustion or close. This includes time the application spends consuming rows. Execute timing ends when the driver returns. Driver pool-wait time is not included. Transaction begin/commit/rollback and preparation are not counted as queries. Driver retries are observed attempts, not guaranteed distinct application operations. Use context-bearing APIs to retain request attribution; context-free operations cannot be attributed to a request.

Wrapped drivers suppress nested profiling when calling native drivers such as pgx, preventing duplicate capture by these integrations. Other OpenTelemetry or application instrumentation remains untouched.

## GORM

Install `integrations/gorm.Plugin{}` with `db.Use` and propagate context using `db.WithContext(r.Context())` or GORM's generic API. The plugin wraps the existing logger, preserves its callbacks and suppresses nested driver profiling. It counts ORM-observed SQL statements and excludes GORM dry runs. Parameter filtering prevents GORM from interpolating bound values for logging.

```go
if err := db.Use(apigorm.Plugin{}); err != nil { return err }
result := db.WithContext(r.Context()).Where("team_id = ?", teamID).Find(&users)
```

GORM operation durations can include ORM work and preloading. Streaming `Row`/`Rows` operations finish their GORM observation before subsequent application scanning, unlike the driver-level rows lifetime. Use one primary profiling level when comparing timings. `WrapLogger(existing)` is available without the plugin, but must be used as an alternative to driver profiling for that pool; only the plugin provides nested-call suppression and dry-run awareness.

## Native pgx

Call `integrations/pgx.Install(config)` on `*pgx.ConnConfig` before connecting. For a pool, pass `poolConfig.ConnConfig`. Installation composes with existing query, batch, COPY, preparation, connection and pool tracers, and repeated installation is harmless.

```go
config, err := pgxpool.ParseConfig(dsn)
if err != nil { return err }
apipgx.Install(config.ConnConfig)
pool, err := pgxpool.NewWithConfig(ctx, config)
```

Normal queries end when result rows close. Batch counts are reported as result callbacks arrive, and each result's measured lifetime starts at `SendBatch`; pipelined lifetimes overlap. `sql.batch` measures the batch lifetime, and `sql.pool.acquire` measures pool acquisition through native pgx hooks. COPY records one operation, without reading copied values. Aborted or abandoned batches may report fewer observed result callbacks than submitted statements. Close result sets and batch results promptly.

## Request summaries

- `Instrumented=false` means no instrumentation has confirmed coverage for the request. It does not mean zero SQL queries.
- If a request is known to use an instrumented database even when it executes no SQL, call `apilog.MarkInstrumented(r.Context())` in the application middleware that supplies that database. An instrumented zero-query request can then be distinguished from missing coverage.
- `QueryCount` is the number of observed query operations; `DuplicateQueries` counts repeated normalized shapes. Repeated shapes are possible N+1 hints, not proof of an ORM relationship problem.
- `SQLDuration` sums observed lifetimes and can exceed request duration when queries overlap. It is not database engine execution time.
- `SQLWallTime` is the union of completed observed intervals inside the request window. Subtracting cumulative SQL duration from request duration is invalid. Neither wall-time remainder nor custom stages should be presented as measured CPU time.
- Unclosed observed query operations and collection limits mark summaries incomplete. The request event finishes when the handler returns and does not wait for background work. Late completion cannot mutate an emitted event.

Use `StartStage(ctx, "remote_api")` to time an application-defined operation and call its returned function once. Stage names should be fixed strings rather than user input. Sampling applies at the request level. Keep instrumentation disabled or sampled for hot paths if measured overhead exceeds your application's budget.

## Verification

The integration tests run SQLite in process through the pure-Go driver and verify prepared queries, transactions, legacy fallback, custom named-value conversion, cancellation and rows closed after request completion. GORM tests execute real SQLite queries with an instrumented underlying driver and assert that operations are not counted twice.

Native pgx callback and composition tests always run. Set `APILOG_TEST_POSTGRES_DSN` to an isolated PostgreSQL test instance to run the additional live query, batch and COPY test. It creates a connection-local temporary table. Tests never print the DSN. No database credentials belong in committed configuration.
