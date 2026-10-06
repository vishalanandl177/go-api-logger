# Changelog

## 1.0.1

- Keep policy failures restrictive and failed sink batches correctly accounted even when `GODEBUG=panicnil=1` restores legacy nil-panic behavior.
- Keep capture-time policy failures restrictive for the rest of the affected request, while allowing subsequent requests to recover.
- Verify recovery after output errors, panics, timeouts, queue saturation and temporary database faults, while preserving application responses and panics.
- Add runnable JSON examples for net/http, Gin, chi and Echo, an annotated log-format guide, a first-request walkthrough, and AI integration references.
- Compile complete documentation examples and validate local documentation links in CI.

## 1.0.0

Initial release of Go API Logger, with a standard-library core and optional modules.

- Capture standard HTTP exchanges and enrich routes from Gin, chi, and Echo v4/v5.
- Mask bounded JSON bodies, headers, and query parameters before asynchronous delivery.
- Apply restrictive endpoint policies, separate storage/export gates, and custom transforms.
- Deliver through independent bounded queues with lifecycle controls and operational health.
- Store, search, aggregate, migrate, and prune logs in PostgreSQL, MySQL, and SQLite.
- Embed an application-authorized dashboard with payload inspection, charts, CSV, and deletion.
- Profile context-attributed SQL through database/sql, pgx, and GORM without storing arguments or results.
- Integrate optional Prometheus metrics, existing OpenTelemetry spans, and Sentry context.
- Provide request/trace correlation and 16 optional detection-only security rules.
- Include runnable examples, reproducible performance measurements, and compatibility tests.

Supported Go lines: 1.26 and 1.27. CI database versions: PostgreSQL 17, MySQL 8.4, and SQLite. Native Fiber/fasthttp adapters are planned follow-up work.

Delivery is best-effort and in memory. Full queues, failed outputs, shutdown deadlines, and process termination can lose events. See the README and operations guide for lifecycle and monitoring requirements.
