$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    docker compose up -d --wait
    if ($LASTEXITCODE -ne 0) { throw 'Database startup failed.' }
    $env:API_LOGGER_TEST_POSTGRES_DSN = 'postgres://apilog:local-test-only@127.0.0.1:55432/apilog_test?sslmode=disable'
    $env:API_LOGGER_TEST_MYSQL_DSN = 'apilog:local-test-only@tcp(127.0.0.1:53306)/apilog_test'
    go test -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Store integration tests failed.' }
} finally {
    Remove-Item Env:API_LOGGER_TEST_POSTGRES_DSN -ErrorAction SilentlyContinue
    Remove-Item Env:API_LOGGER_TEST_MYSQL_DSN -ErrorAction SilentlyContinue
    docker compose down
    Pop-Location
}
