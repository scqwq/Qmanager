$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
$notice = [Text.StringBuilder]::new()
[void]$notice.AppendLine('Qman third-party notices — generated from the locked local dependencies')
$modules = go list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' . | Sort-Object -Unique
if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate Go dependencies' }
foreach ($moduleLine in $modules) {
    $parts = $moduleLine.Split('|')
    if ($parts.Count -lt 3 -or -not $parts[1] -or -not $parts[2]) { continue }
    [void]$notice.AppendLine("`n=== $($parts[0]) $($parts[1]) ===")
    $licenses = Get-ChildItem -LiteralPath $parts[2] -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)' }
    foreach ($license in $licenses) { [void]$notice.AppendLine([IO.File]::ReadAllText($license.FullName)) }
    if (-not $licenses) { [void]$notice.AppendLine('See the upstream module for license terms.') }
}
$goRootPath = go env GOROOT
[void]$notice.AppendLine("`n=== Go runtime ===")
[void]$notice.AppendLine([IO.File]::ReadAllText((Join-Path $goRootPath 'LICENSE')))
$npmPackages = node -e 'const p = require("./frontend/package-lock.json"); for (const [path,v] of Object.entries(p.packages)) { if (path && !v.dev) console.log(JSON.stringify({path,version:v.version,license:v.license})); }'
if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate npm dependencies' }
foreach ($packageLine in $npmPackages) {
    $packageEntry = $packageLine | ConvertFrom-Json
    $packagePath = Join-Path (Join-Path $projectRoot 'frontend') $packageEntry.path
    if (-not (Test-Path -LiteralPath $packagePath)) { continue }
    [void]$notice.AppendLine("`n=== npm $($packageEntry.path) $($packageEntry.version) ===")
    $licenses = Get-ChildItem -LiteralPath $packagePath -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|NOTICE)' }
    foreach ($license in $licenses) { [void]$notice.AppendLine([IO.File]::ReadAllText($license.FullName)) }
    if (-not $licenses) { [void]$notice.AppendLine("License: $($packageEntry.license)") }
}
$output = Join-Path $projectRoot 'build/bin/THIRD_PARTY_NOTICES.txt'
[IO.File]::WriteAllText($output,$notice.ToString(),[Text.UTF8Encoding]::new($false))
Copy-Item -LiteralPath 'docs/USER_GUIDE.md' -Destination 'build/bin/使用说明.md' -Force
