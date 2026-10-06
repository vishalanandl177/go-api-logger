# Contributing

Install Go 1.26 or 1.27 and clone this repository. The core is standard-library-only. Optional modules are maintained together, with independent release tags.

```sh
go work init . ./storage ./integrations ./cmd ./examples
bash scripts/verify.sh --race
```

On Windows, run `./scripts/verify.ps1 -Portable` when a C compiler is unavailable. Full race validation runs in Linux CI. `storage/compose.yaml` starts disposable PostgreSQL and MySQL test services. Never point integration tests at production databases.

Use `gofmt`, test the affected module, then run the complete verification script. Include regression tests for behavior changes and update the relevant guide. Do not retain request/context objects in background queues, add unbounded buffers, log raw malformed JSON, or include payloads in telemetry labels.

Useful commands:

```sh
go test ./... -count=1
go test -race ./...
go test -run='^$' -fuzz=FuzzSanitizeJSON -fuzztime=30s .
go test -bench=. -benchmem . ./httpmw
```

Documentation checks require Python 3.10 or newer:

```sh
python scripts/check-docs.py --compile checkout
python scripts/check-docs.py --compile released
```

The checker validates repository-relative links and heading anchors, and compiles complete Go fenced examples in isolated modules. It does not execute snippets or shell commands from documentation. `checkout` uses the current module sources; `released` resolves the pinned modules in `scripts/_releasecheck/go.mod` without replacements. The latter can require network access to download published dependencies. Runnable examples are also built and tested by the normal module verification scripts.

CI tests both supported Go release lines, Windows/macOS portable behavior, all three SQL stores, security scanning, and fuzz seeds. A stable release requires passing tests and successful installation into an unrelated clean module.

The [published consumer check](scripts/_releasecheck/README.md) deliberately disables the workspace and resolves released versions without replacements. CI runs it separately and scans each module's independent dependency graph. Normal development tests use the workspace to exercise source changes across modules together.

Please open an issue describing the use case before adding a new integration. Custom server integrations should use `Logger.Begin`, shared context helpers, and `Exchange.Finish`, and pass the HTTP behavior and privacy conformance scenarios.
