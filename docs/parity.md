# Feature parity and verification

This matrix maps the implemented DRF-API-Logger capabilities inspected for this project to their Go equivalents. The packages use Go-native lifecycle, context, and storage interfaces. Fiber/fasthttp adapters are explicitly follow-up work.

| Capability | Implementation | Acceptance evidence |
| --- | --- | --- |
| Request/response metadata and bounded bodies | `httpmw`, `Body`, `Event` | Middleware pass-through, body states, HTTP2/SSE/upgrade/gzip tests |
| Recursive secret masking and query/header masking | Root sanitizer | Regression tables and `FuzzSanitizeJSON` |
| Endpoint selection and restrictive policies | Root policy evaluator | Capture-time routes, failure fallback, export-gate tests |
| Background batching and custom subscribers | Root logger, JSON/Slog/SinkFunc | Capacity/byte limits, output isolation, failure and shutdown tests |
| SQL storage and programmatic access | `storage` | Shared SQLite/Postgres/MySQL live contract suite |
| Search/filter/sort/detail dashboard | `dashboard` | HTTP tests and desktop/mobile browser smoke |
| Three lazy analytics charts | Store aggregates and embedded dashboard | Filter-aware chart, date, status, SQL-profile tests |
| CSV and authorized deletion | `dashboard` | Permission, policy, CSRF, escaping and formula tests |
| SQL profiling and diagnostic hints | Root collector and integrations/sql/pgx/gorm | Concurrency/overlap, statements/transactions/rows, deduplication tests |
| Correlation and application context | Root context and integration helpers | HTTP correlation and tracer-context tests |
| Optional metrics and observability | `integrations` | Registry, label, lifecycle, existing SDK tests |
| All 16 security hints | Root detector | Per-rule fixture, bounded TTL and inspection opt-in tests |
| Retention, migrations, doctor | `storage`, `cmd`, `Logger.Diagnose` | Explicit migration, dry-run, cutoff, CLI severity tests |

## Compatibility boundaries

- Go 1.26 and 1.27; standard HTTP, Gin v1, chi v5, Echo v4/v5.
- PostgreSQL 17 and MySQL 8.4 service tests, plus pure-Go SQLite; other server versions are not yet in the CI compatibility claim.
- Database profiling requires the request context and an instrumented client. Uninstrumented queries are unavailable, not a measured zero.
- Handler time is not client/network latency, CPU time, or a universal breakdown of every framework middleware. Explicit stage hooks cover application-specific stages.
- Concurrent query durations are cumulative work. The profile separately records the union of observed query intervals and never subtracts cumulative SQL duration from request duration.
- Metrics use template/allowlisted routes, not raw path labels. A hijacked connection can have an unknown HTTP status if its handshake bypasses ResponseWriter.
- In-memory delivery is best-effort. Bounded queues intentionally replace DRF's unbounded background queue.
- Per-event export policy is persisted in Go. Correlation fields are consistent across outputs, including database records.

## Release gate

A stable release requires all CI jobs to pass, live PostgreSQL/MySQL tests without skips, independent module installation, race/fuzz checks, documented benchmark evidence, and verified remote tags. Local checks alone do not establish these external gates.
