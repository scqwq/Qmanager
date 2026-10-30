param(
    [Parameter(Mandatory)][string]$Source,
    [Parameter(Mandatory)][string]$Output,
    [Parameter(Mandatory)][string]$OwnerAccount
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
# The executor may be an ephemeral sandbox account. Require the intended human
# account explicitly so the private copy remains accessible after this session.
$ownerSid = ([Security.Principal.NTAccount]::new($OwnerAccount)).Translate([Security.Principal.SecurityIdentifier])
function Assert-NoLinks([string]$Path) {
    $cursor = [IO.Path]::GetFullPath($Path)
    while ($cursor) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'LINK_PATH_REJECTED' }
        }
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ($parent -eq $cursor) { break }
        $cursor = $parent
    }
}
Assert-NoLinks $Source
Assert-NoLinks $Output
$sourceFull = [IO.Path]::GetFullPath($Source).TrimEnd('\')
$outputFull = [IO.Path]::GetFullPath($Output).TrimEnd('\')
if ($outputFull.Equals($sourceFull, [StringComparison]::OrdinalIgnoreCase) -or
    $outputFull.StartsWith($sourceFull + '\', [StringComparison]::OrdinalIgnoreCase) -or
    $sourceFull.StartsWith($outputFull + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'OVERLAPPING_PATHS' }
if (Test-Path -LiteralPath $outputFull) { throw 'OUTPUT_ALREADY_EXISTS' }
$entries = @(Get-ChildItem -LiteralPath $sourceFull -Force)
if (@($entries | Where-Object { $_.PSIsContainer -or ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) }).Count) { throw 'UNEXPECTED_SOURCE_ENTRY' }
$bytes = [long](($entries | Measure-Object Length -Sum).Sum)
if ($bytes -gt 5GB) { throw 'TEST_COPY_BUDGET_EXCEEDED' }
$drive = [IO.DriveInfo]::new([IO.Path]::GetPathRoot($outputFull))
if ($drive.AvailableFreeSpace -lt ($bytes * 2 + 1GB)) { throw 'INSUFFICIENT_SPACE' }
$handles = [Collections.Generic.List[object]]::new()
$rows = [Collections.Generic.List[object]]::new()
try {
    # Read sharing permits readers but rejects concurrent writable/deletable handles.
    foreach ($entry in $entries) {
        $stream = [IO.File]::Open($entry.FullName, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        $handles.Add([pscustomobject]@{Entry=$entry;Stream=$stream})
    }
    [IO.Directory]::CreateDirectory($outputFull) | Out-Null
    $userSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = Get-Acl -LiteralPath $outputFull
    # Windows AppContainer access requires account and package/capability grants.
    # Preserve only the parent's specific application grants, never broad groups
    # such as Users, Everyone, or ALL APPLICATION PACKAGES.
    $capabilityRules = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) |
        Where-Object { $_.AccessControlType -eq 'Allow' -and
            ($_.IdentityReference.Value.StartsWith('S-1-15-3-') -or
            ($_.IdentityReference.Value.StartsWith('S-1-15-2-') -and $_.IdentityReference.Value -notin @('S-1-15-2-1', 'S-1-15-2-2'))) })
    $acl.SetAccessRuleProtection($true, $false)
    $rule = [Security.AccessControl.FileSystemAccessRule]::new($userSid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($rule)
    if ($ownerSid.Value -ne $userSid.Value) {
        $ownerRule = [Security.AccessControl.FileSystemAccessRule]::new($ownerSid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($ownerRule)
    }
    foreach ($capability in $capabilityRules) {
        $capabilityRights = $capability.FileSystemRights
        if ([long]$capabilityRights -band 0x10000000) {
            # Inherited GENERIC_ALL must be mapped before constructing a rule.
            $capabilityRights = [Security.AccessControl.FileSystemRights]::FullControl
        }
        $capabilityRule = [Security.AccessControl.FileSystemAccessRule]::new($capability.IdentityReference, $capabilityRights, 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($capabilityRule)
    }
    Set-Acl -LiteralPath $outputFull -AclObject $acl
    foreach ($held in $handles) {
        $inputStream = $held.Stream
        $sha = [Security.Cryptography.SHA256]::Create()
        try {
            $before = [Convert]::ToHexString($sha.ComputeHash($inputStream)).ToLowerInvariant()
            $inputStream.Position = 0
            $destination = Join-Path $outputFull $held.Entry.Name
            $outputStream = [IO.File]::Open($destination, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
            try { $inputStream.CopyTo($outputStream, 1048576); $outputStream.Flush($true) } finally { $outputStream.Dispose() }
            $inputStream.Position = 0
            $after = [Convert]::ToHexString($sha.ComputeHash($inputStream)).ToLowerInvariant()
            $copyHash = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($before -ne $after -or $before -ne $copyHash) { throw 'HASH_MISMATCH' }
            $inputStream.Position = 0
            $header = [byte[]]::new(16)
            $headerLength = $inputStream.Read($header, 0, 16)
            $plain = $headerLength -eq 16 -and [Text.Encoding]::ASCII.GetString($header) -eq "SQLite format 3`0"
            $rows.Add([pscustomobject]@{Name=$held.Entry.Name;Bytes=$inputStream.Length;SHA256=$before;PlainSQLiteHeader=$plain})
        } finally { $sha.Dispose() }
    }
    $endNames = @(Get-ChildItem -LiteralPath $sourceFull -Force | Select-Object -ExpandProperty Name | Sort-Object)
    $startNames = @($entries.Name | Sort-Object)
    if (@(Compare-Object $startNames $endNames).Count) { throw 'SOURCE_ENTRY_SET_CHANGED' }
    $rows | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $outputFull 'evidence.json') -Encoding utf8
    [pscustomobject]@{Status='COPIED_HASH_VERIFIED';FileCount=$rows.Count;Bytes=$bytes;PlainSQLiteFiles=@($rows | Where-Object PlainSQLiteHeader | Select-Object -ExpandProperty Name);NonPlainDatabaseCount=@($rows | Where-Object { $_.Name.EndsWith('.db') -and -not $_.PlainSQLiteHeader }).Count;Output=$outputFull} | ConvertTo-Json -Depth 3
} finally {
    foreach ($held in $handles) { $held.Stream.Dispose() }
}
