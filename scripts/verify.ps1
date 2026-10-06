param([switch]$Race, [switch]$Portable)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$modules = @('.', 'storage', 'integrations', 'cmd', 'examples')
if ($Portable) { $modules = @('.', 'storage', 'cmd', 'examples') }
foreach ($module in $modules) {
    $moduleName = $module
    if ($module -eq '.') { $moduleName = 'root' }
    $buildDir = [System.IO.Path]::GetFullPath((Join-Path $repoRoot ".artifacts/build/$moduleName"))
    $workspacePrefix = $repoRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
    if (-not $buildDir.StartsWith($workspacePrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw 'Build output must stay within the repository.'
    }
    New-Item -ItemType Directory -Path $buildDir -Force | Out-Null
    Push-Location (Join-Path $repoRoot $module)
    try {
        $testArgs = @('test', '-count=1', './...')
        if ($Race) { $testArgs = @('test', '-race', '-count=1', './...') }
        & go @testArgs
        if ($LASTEXITCODE -ne 0) { throw "Tests failed in $module." }
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "Vet failed in $module." }
        $packageNames = @(go list -f '{{.Name}}' ./...)
        if ($LASTEXITCODE -ne 0) { throw "Package discovery failed in $module." }
        if ($packageNames -contains 'main') {
            go build -o $buildDir ./...
        } else {
            go build ./...
        }
        if ($LASTEXITCODE -ne 0) { throw "Build failed in $module." }
    } finally { Pop-Location }
}
