param([switch]$Race, [switch]$Portable)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$modules = @('.', 'storage', 'integrations', 'cmd', 'examples')
if ($Portable) { $modules = @('.', 'storage', 'cmd', 'examples') }
foreach ($module in $modules) {
    Push-Location (Join-Path $repoRoot $module)
    try {
        $testArgs = @('test', '-count=1', './...')
        if ($Race) { $testArgs = @('test', '-race', '-count=1', './...') }
        & go @testArgs
        if ($LASTEXITCODE -ne 0) { throw "Tests failed in $module." }
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "Vet failed in $module." }
        go build ./...
        if ($LASTEXITCODE -ne 0) { throw "Build failed in $module." }
    } finally { Pop-Location }
}
