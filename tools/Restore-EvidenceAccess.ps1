param(
    [Parameter(Mandatory)][string]$CopyDirectory,
    [Parameter(Mandatory)][string]$OwnerAccount
)
$ErrorActionPreference = 'Stop'
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw '请在管理员 PowerShell 中运行此脚本。' }
$target = [IO.Path]::GetFullPath($CopyDirectory).TrimEnd('\')
$expected = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Temp\QQArchive-tests\014494bc-7b3b-45d4-b2ae-33fc9774411e\nt_db'
if (-not $target.Equals($expected, [StringComparison]::OrdinalIgnoreCase)) { throw '此脚本只允许修复本次生成的已知副本目录。' }
$cursor = [IO.Path]::GetDirectoryName($target)
while ($cursor) {
    if ((Get-Item -LiteralPath $cursor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw '拒绝链接路径。' }
    $cursor = [IO.Path]::GetDirectoryName($cursor)
}
$sid = ([Security.Principal.NTAccount]::new($OwnerAccount)).Translate([Security.Principal.SecurityIdentifier]).Value
# Operate on one known application-owned directory. No recursive ownership or
# reset command, no source path, no database/content modifications.
& takeown.exe /F $target /A
if ($LASTEXITCODE) { throw '目录所有权恢复失败。' }
if ((Get-Item -LiteralPath $target -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw '拒绝链接目录。' }
& icacls.exe $target /grant ('*' + $sid + ':(OI)(CI)F')
if ($LASTEXITCODE) { throw '用户权限恢复失败。' }
# Old children may have protected ACLs: repair each validated flat file rather
# than relying on directory inheritance. Never recurse or follow links.
$files = @(Get-ChildItem -LiteralPath $target -Force)
if ($files.Count -ne 31 -or @($files | Where-Object { $_.PSIsContainer -or ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) }).Count) {
    throw '副本结构不符合预期：应为 30 个数据文件和 evidence.json，无子目录或链接。'
}
$parentAcl = Get-Acl -LiteralPath ([IO.Path]::GetDirectoryName($target))
$appSids = @($parentAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) |
    Where-Object { $_.AccessControlType -eq 'Allow' -and
        ($_.IdentityReference.Value.StartsWith('S-1-15-3-') -or
         ($_.IdentityReference.Value.StartsWith('S-1-15-2-') -and $_.IdentityReference.Value -notin @('S-1-15-2-1', 'S-1-15-2-2'))) } |
    ForEach-Object { $_.IdentityReference.Value } | Sort-Object -Unique)
foreach ($appSid in $appSids) {
    & icacls.exe $target /grant ('*' + $appSid + ':(OI)(CI)RX') | Out-Null
    if ($LASTEXITCODE) { throw '应用只读权限修复失败。' }
}
foreach ($file in $files) {
    if (-not [IO.Path]::GetDirectoryName($file.FullName).Equals($target, [StringComparison]::OrdinalIgnoreCase)) { throw '拒绝越界文件。' }
    & takeown.exe /F $file.FullName /A | Out-Null
    if ($LASTEXITCODE) { throw '副本文件所有权修复失败。' }
    & icacls.exe $file.FullName /grant ('*' + $sid + ':F') | Out-Null
    if ($LASTEXITCODE) { throw '副本文件用户权限修复失败。' }
    foreach ($appSid in $appSids) {
        & icacls.exe $file.FullName /grant ('*' + $appSid + ':RX') | Out-Null
        if ($LASTEXITCODE) { throw '副本文件应用只读权限修复失败。' }
    }
}
$evidence = Join-Path $target 'evidence.json'
$rows = @(Get-Content -LiteralPath $evidence -Raw | ConvertFrom-Json)
if ($rows.Count -ne 30) { throw '证据清单数量不符。' }
$seen = @{}
$total = 0L
foreach ($row in $rows) {
    if ([IO.Path]::GetFileName($row.Name) -ne $row.Name -or $row.Name -eq 'evidence.json' -or $seen.ContainsKey($row.Name)) { throw '证据清单文件名无效。' }
    $seen[$row.Name] = $true
    $filePath = Join-Path $target $row.Name
    if ((Get-Item -LiteralPath $filePath).Length -ne $row.Bytes -or
        (Get-FileHash -LiteralPath $filePath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $row.SHA256) { throw '副本大小或哈希不匹配，停止读取验证。' }
    $total += $row.Bytes
}
[pscustomobject]@{ Status = 'COPY_HASH_REVERIFIED'; Count = $rows.Count; Bytes = $total } | ConvertTo-Json
Write-Host '副本文件权限及哈希核对完成；请把以上结果发回聊天。'