# Operations

Run the optional CLI from its module:

```sh
cd cmd
mkdir -p ../.artifacts
go build -o ../.artifacts/apilog ./apilog
export API_LOGGER_DSN='api-logs.db'
../.artifacts/apilog migrate --driver sqlite
../.artifacts/apilog doctor --driver sqlite --format json
../.artifacts/apilog prune --driver sqlite --days 30 --dry-run
../.artifacts/apilog prune --driver sqlite --days 30
```

PowerShell uses `New-Item -ItemType Directory -Force ../.artifacts`, `$env:API_LOGGER_DSN = 'api-logs.db'` and `..\.artifacts\apilog.exe` after building `go build -o ../.artifacts/apilog.exe ./apilog`. Use `--driver postgres` or `--driver mysql` with the corresponding driver DSN. `--dsn-env NAME` reads a different environment variable. Connection strings are not accepted as positional arguments and are never printed in diagnostics. The CLI opens and closes its own pool; library callers retain ownership of theirs.

## Migration and readiness

`migrate` is explicit and versioned. Run it once as part of deployment before enabling database logging or opening the dashboard. The process needs schema creation privileges. Runtime applications can use a narrower account with the required read/write permissions. A later schema version fails readiness instead of being silently downgraded.

`doctor` checks connectivity, exact schema version, and required columns without reading payloads or changing tables. It emits coded `ok`, `warning`, or `error` results in text or JSON. `--fail-level error` is the default; `--fail-level warning` also fails on warnings. An error exits 1; invalid command options exit 2; successful commands exit 0. `--timeout 30s` bounds the entire database operation and can be changed.

A standalone doctor process cannot inspect a running application's worker, queue, policies, body limits or profiling configuration. It explicitly reports `RUNTIME_NOT_INSPECTED` as a warning. Inspect the application's in-process `logger.Health()` together with the configuration validated by logger construction. Monitor worker availability, accepted/delivered/dropped/failed counts, queued events/bytes, in-flight batches and write duration. The CLI does not start a dummy worker or report its health as the application's health.

## Retention

Prune supports exactly one of `--days N` or `--before YYYY-MM-DD/RFC3339`. Calendar dates are interpreted as midnight UTC. The cutoff is exclusive, so an event exactly at the cutoff survives. Use `--dry-run` first, and choose a retention period that matches the data owner's requirements. `--batch-size` defaults to 1000 and accepts 1 through 1000. Schedule the command using the application's deployment scheduler; the library does not install a system job.

Deletion is committed batch by batch and can be interrupted. Reports include the initial matching row count, actual deleted count, and dry-run mode. A failed run can have deleted earlier batches; rerun with the same cutoff after investigating. Backups and replicas may retain data after primary-row deletion and require their own retention controls.

## Delivery and shutdown

The application owns HTTP server shutdown, logger draining and pool closure. Stop accepting HTTP requests, allow existing requests to finish, call the logger's deadline-bound shutdown, then close database pools. Never close a pool while its logger worker is still writing. A forced process exit can lose queued events.

Delivery is best-effort. Queue saturation and database outages can drop logs; the logger does not block customer requests waiting for database recovery. Alert on growing queues, dropped/failed events and worker failure. Use another system when durable audit delivery is a requirement. Keep storage writes separate from the application's query profiling and protect metrics/dashboard endpoints with application authorization.

SQLite is suitable for local/single-instance use with a local file and serialized writes. Concurrent processes, network filesystems, sustained ingestion and broad payload searches need deployment-specific testing. PostgreSQL or MySQL is usually the appropriate choice for a shared operational log store. Configure connection pool limits, TLS, backup, encryption and database access within the application and infrastructure.
