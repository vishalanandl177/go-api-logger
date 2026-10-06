# Runnable examples

The `standard` example runs a REST API, an instrumented application SQLite database, a separate log SQLite database, an authenticated embedded dashboard, and protected Prometheus metrics. It binds only to `127.0.0.1`. Its SQL is parameterized and its responses never expose database errors.

From the repository root, create a development workspace once after cloning:

```sh
go work init . ./storage ./integrations ./cmd ./examples
cd examples
```

Set `API_LOGGER_ADMIN_PASSWORD` in your terminal to a private password of at least 16 characters. The program does not display it. Start with an explicit schema setup:

```sh
go run ./standard -migrate
```

Subsequent starts use `go run ./standard`. Schemas are never created implicitly. Files live in `examples/demo-data` unless `-data-dir` is set. Choose another loopback port using `-port 8081`.

Open [the local dashboard](http://127.0.0.1:8080/api-logs/) and authenticate as `admin` with your environment-provided password. The demo permits viewing and CSV export but deliberately denies dashboard deletion. `/internal/metrics` and `/internal/health` use the same authentication. Dashboard, internal and health routes are excluded from API logging, so credentials and self-observation requests are not persisted.

Create representative traffic:

```sh
curl -H 'Content-Type: application/json' -d '{"name":"Example item","token":"demonstration-secret"}' http://127.0.0.1:8080/items
curl http://127.0.0.1:8080/items
curl http://127.0.0.1:8080/items/1
curl http://127.0.0.1:8080/slow
curl http://127.0.0.1:8080/error
```

The token is a demonstration value, is masked in captured JSON, and is never stored in the application items table. `/slow` waits 250 ms before querying; `/error` returns a deliberate HTTP 500. Logs flush every second or at the batch threshold. SQL profiling requires context-aware calls on the instrumented application pool; the separate logger pool is excluded from profiling.

Ctrl+C drains the HTTP server, then drains the logger, then closes both database pools. If draining exceeds its deadline, the program reports incomplete shutdown. Copy the explicit resource ordering when adapting the example. The demo's Basic authentication is suitable for local learning; an externally reachable deployment needs the application's real authentication and TLS boundary.

Run `go test ./...` here to exercise a real HTTP/SQLite workflow, body redaction, SQL profiling, dashboard authentication, and excluded internal routes. `go run ./benchmark` runs the separate measurement harness documented in [performance](../docs/performance.md).
