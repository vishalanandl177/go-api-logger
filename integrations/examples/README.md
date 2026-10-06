# Runnable HTTP integration examples

Use Go 1.26 or newer. From the repository's `integrations` directory, run one application:

| Server | Command | Main program |
| --- | --- | --- |
| Standard `net/http` | `go run ./examples/nethttp` | [nethttp/main.go](nethttp/main.go) |
| Gin v1 | `go run ./examples/gin` | [gin/main.go](gin/main.go) |
| chi v5 | `go run ./examples/chi` | [chi/main.go](chi/main.go) |
| Echo v4 | `go run ./examples/echov4` | [echov4/main.go](echov4/main.go) |
| Echo v5 | `go run ./examples/echov5` | [echov5/main.go](echov5/main.go) |

All examples listen on `127.0.0.1:8080`, so run one at a time. They share a [runner](internal/run/run.go) that creates the logger, wraps the router, starts the HTTP server, and shuts down HTTP before draining the logger on Ctrl+C or SIGTERM. The examples use batch size 1 so the first event appears promptly on stdout. Default production batching is 50 events or 10 seconds.

Open `http://127.0.0.1:8080/users/42` or send a request from another terminal:

```sh
curl -i -H 'Content-Type: application/json' \
  -d '{"name":"Ada","token":"demo-token-not-a-real-credential"}' \
  http://127.0.0.1:8080/users
```

PowerShell:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/users -Method Post `
  -ContentType 'application/json' `
  -Body '{"name":"Ada","token":"demo-token-not-a-real-credential"}'
```

The handler consumes the JSON, responds with HTTP 201 and `{"created":true}`, and the log contains `"token":"***FILTERED***"`. Invalid JSON returns HTTP 400. Echo also has `GET /error` to demonstrate its central error handler returning HTTP 422 before capture finishes.

The standard HTTP example is placed here to compare all server variants with the same runner. An application using only `net/http` needs only the core module, not the integrations module.

To copy this into your own application, follow the [HTTP integration guide](../../docs/http-integration.md). The guide includes exact imports and versioned installs, existing-server recipes, framework middleware order, body-state explanations, and native adapter boundaries. The separate [SQLite application](../../examples/README.md) demonstrates persistence and a protected dashboard.

Verify all examples from `integrations`:

```sh
go test ./examples/...
go vet ./examples/...
```

Acceptance tests send the documented GET and POST requests through each actual router, check route templates and status codes, wait for asynchronous delivery, and verify that complete and malformed request bodies are safely handled.
