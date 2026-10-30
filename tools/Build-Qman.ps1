param([string]$OutputName = 'Qman.exe')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
$env:GOPATH = Join-Path $projectRoot '.local\go'
$env:GOMODCACHE = Join-Path $projectRoot '.local\gomodcache'
$env:GOCACHE = Join-Path $projectRoot '.local\gocache'
$env:GOBIN = Join-Path $projectRoot '.local\bin'
$env:npm_config_cache = Join-Path $projectRoot '.local\npm-cache'
$wailsTool = Join-Path $env:GOBIN 'wails.exe'
if (-not (Test-Path -LiteralPath $wailsTool)) {
    New-Item -ItemType Directory -Path $env:GOBIN -Force | Out-Null
    go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
    if ($LASTEXITCODE -ne 0) { throw 'Wails installation failed' }
}
Push-Location -LiteralPath (Join-Path $projectRoot 'frontend')
try {
    npm.cmd ci --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) { throw 'Frontend dependency installation failed' }
    npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
} finally { Pop-Location }
& $wailsTool build -skipbindings -s -o $OutputName
if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
& (Join-Path $PSScriptRoot 'Write-Notices.ps1')
