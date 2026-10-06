# Failure isolation and recovery

Go API Logger treats log delivery as best-effort background work. A failed output must not replace an API response. Recoverable sink and logging-hook panics are contained, failed writes are counted, and the same logger can deliver subsequent events when the output becomes healthy again.

This is recovery of future logging, not replay of lost records or a promise that an application process can never crash. The library has no durable spool and does not retry a failed batch automatically.

## What happens during a fault

| Fault | Behavior | Recovery |
| --- | --- | --- |
| Sink returns an error or panics | Batch is counted in `Failed`; API response is unchanged. | The worker continues with subsequent batches. No logger recreation is needed. |
| A cooperative sink exceeds its write deadline | Its context is canceled; a returned error is counted as a failed batch. | Later batches get fresh contexts and can succeed. |
| Database temporarily unavailable or locked | Current transaction/batch can fail; its failure is counted. | Subsequent batches use fresh transactions and can succeed after the database recovers. |
| Queue reaches the event or byte limit | New events for that output are dropped and counted in `Dropped`. | New events are accepted again when capacity becomes available. |
| One output blocks while another is healthy | Each has its own queue and worker. | The healthy output keeps delivering; the blocked output remains bounded. |
| Policy callback fails | Sensitive headers, query values and bodies are omitted; export stays disabled for that exchange even if a later callback succeeds. | Later requests invoke the policy again. Healthy returns restore normal configured behavior. |
| Transform callback fails | The affected event is skipped for output. | Later events can proceed when the transform succeeds. |
| Sanitizer callback fails | Body data is omitted and marked invalid; raw bytes are not used as a fallback. | Later valid payloads can be captured. |
| Observer callback panics | The panic is contained around the callback. | The API and outputs continue; the observer is called again on later events. |
| Request-ID generator panics or returns an invalid ID | The HTTP adapter falls back to generating a request ID. | Request handling continues. |
| Route resolver panics | The affected exchange is excluded from capture/output. | Other requests are independent. |
| Logger shutdown starts | New output acceptance stops and queued work drains up to the deadline. | Shutdown is intentional and terminal; create a new logger only for a new application lifecycle. |

All output work is bounded by the configured queue limits. The default is 1,024 events or 16 MiB of serialized event data per output, including in-flight work, with batches of 50 and a five-second write deadline. Serialized queue bytes are not a cap on the entire process heap.

## Custom code must cooperate

The sink contract includes honoring context cancellation. Go cannot forcibly interrupt an arbitrary callback that blocks forever. The library does not start a new goroutine for each timeout, which would leak goroutines and retained events during an outage. A permanently blocked sink can therefore occupy its own worker until it returns; its queue drops new events when full. Shutdown can return a deadline error while that callback remains blocked.

Policy, observer, transformation, sanitizer and route callbacks run synchronously. Keep them bounded and free of network I/O. Panic recovery does not limit their execution time. Use a sink for slow output work and configure its client/driver timeouts as well as the logger deadline.

Callbacks must not invoke `os.Exit`, `runtime.Goexit`, trigger fatal runtime faults, mutate shared data without synchronization, or corrupt memory through unsafe code. Recovery applies on the goroutine invoking the callback; a panic in another goroutine started by custom code is outside that boundary. A custom HTTP adapter must preserve the [adapter contract](architecture.md). Passing a nil logger to the HTTP middleware is a setup error and panics immediately, before serving requests; check constructor errors during startup.

The library deliberately preserves application-handler panics. Install your application's recovery middleware inside the outer capture wrapper if you want to render HTTP 500 responses. Logging must not hide a bug in the application or change its recovery contract.

## Detect an outage and its recovery

Use `logger.Health()` on the running instance and inspect each output separately. The [operations guide](operations.md) has a complete health helper. `logger.Diagnose()` provides a readable summary; it does not restart workers or query databases.

For an output called `database`, a simple failure-and-recovery sequence can look like:

```json
{"Accepted":50,"Delivered":0,"Failed":50,"Dropped":0,"Queued":0,"InFlight":0,"WorkerRunning":true}
```

After the database recovers and the next batch succeeds:

```json
{"Accepted":100,"Delivered":50,"Failed":50,"Dropped":0,"Queued":0,"InFlight":0,"WorkerRunning":true}
```

These are abbreviated illustrative output-health snapshots. Counters are cumulative for the logger lifetime. The historical `Failed: 50` remains after recovery. Alert on counter increases, persistent queue pressure, a worker stopping unexpectedly, or delivery not progressing. Do not endlessly restart a healthy process because its cumulative failure count is nonzero.

Successful HTTP responses do not prove that logs were persisted. `Flush` waits for accepted events to settle as delivered, failed, or dropped; inspect counters after it returns. Increase queue capacity only to absorb a measured temporary burst. If storage is continuously slower than incoming traffic, tune storage, reduce capture, or change output capacity based on measurements.

## Startup and process-level recovery

Configuration errors and initial database/schema errors are returned to the application. Choose an explicit startup policy: fail startup if logging is mandatory, or configure an approved fallback output when availability is the priority. The library does not silently switch destinations, enable insecure output, or create schemas to hide an error.

After startup, keep application health separate from log-output health if a logging outage should not remove healthy API servers from service. Applications that require durable audit records need a durability design beyond this in-memory, best-effort logger.

Database recovery assumes the application keeps its pool open. An application-closed `*sql.DB` remains closed; the logger does not reopen caller-owned resources. A connection loss during commit can leave an ambiguous outcome: rows may already be stored even when the batch returns an error. This is another reason failed batches are not automatically replayed.

Out-of-memory termination, fatal Go runtime errors, an OS/process kill, host failure and machine restart are outside panic recovery. Use the service manager already operating the application, such as systemd, a container restart policy, Kubernetes, or a Windows service supervisor. Configure bounded restart backoff and application readiness checks. The library itself should not fork or restart the process hosting your API.

## Executable failure checks

The repository includes fault-injection tests for these boundaries:

- [Output recovery tests](../reliability_test.go): sink errors, normal and nil panics, deadlines, separate outputs, bounded outage queues, and concurrent flush/shutdown.
- [Policy recovery tests](../policy_recovery_test.go): policy errors/panics minimize sensitive data, then later successful requests recover.
- [HTTP fault-isolation tests](../httpmw/fault_isolation_test.go): hook faults leave API responses unchanged and preserve application panics.
- [Storage tests](../storage): fresh transactions, failed batches, and recovery after database faults.

Run all modules and the race detector using [the verification instructions](../CONTRIBUTING.md). Recovery tests are evidence for the specific recoverable failures they inject, not a universal crash-proof guarantee.
