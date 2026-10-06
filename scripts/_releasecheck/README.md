# Published-module release check

Run this standalone consumer after the root, storage and integrations v1.0.1 tags are publicly available. From this directory:

```sh
GOWORK=off go mod tidy
GOWORK=off go test -count=1 -v ./...
GOWORK=off go list -m -json all
```

PowerShell:

```powershell
$env:GOWORK = 'off'
go mod tidy
go test -count=1 -v ./...
go list -m -json all
```

Check that all three project modules resolve to v1.0.1 and have no `Replace` field. Keep local replacements out of this module. Dependency resolution needs network access initially; the test itself uses temporary SQLite databases and a local HTTP test server.

Blank imports compile Gin, chi, Echo v4/v5, pgx, GORM, Prometheus, OpenTelemetry and Sentry adapters. Runtime assertions cover explicit storage migration, HTTP request/response preservation, body/header/query masking, instrumented SQLite queries, log delivery, dashboard authorization, rendered list/detail/assets and prevention of recursive dashboard logging.

Repository CI uses a workspace to test current checkout modules together. This release check deliberately uses `GOWORK=off` to verify published module dependencies and embedded assets as a downstream consumer. CI runs it in a separate step after independent module vulnerability scans. The leading underscore and its own `go.mod` keep it outside the development workspace and root `go list ./...`, so it adds no dependencies to the core module.
