# The host embeds this fixed loader; request data arrives in $r as Base64 JSON.
# No helper is loaded from the installation being replaced.
$ErrorActionPreference = 'Stop'
function Fail([string]$Code) { throw [InvalidOperationException]::new($Code) }
function Length([string]$Text) {
    $count = 0
    for ($index=0; $index -lt $Text.Length; $index++) {
        if ([char]::IsHighSurrogate($Text[$index])) {
            if ($index + 1 -ge $Text.Length -or -not [char]::IsLowSurrogate($Text[$index + 1])) { Fail 'INVALID_ARGUMENT' }
            $index++
        } elseif ([char]::IsLowSurrogate($Text[$index])) { Fail 'INVALID_ARGUMENT' }
        $count++
    }
    return $count
}
function Check([string]$Path) {
    if ($Path -notmatch '^[A-Za-z]:\\' -or (Length $Path) -gt 32760 -or
        $Path -match '[\x00-\x1f\x7f<>"|?*]' -or $Path.Substring(2).Contains(':')) { Fail 'INVALID_ARGUMENT' }
    $current = $Path.Substring(0, 3)
    foreach ($part in $Path.Substring(3).Split('\')) {
        if ($part -eq '' -or $part -eq '.' -or $part -eq '..' -or $part.TrimEnd(' ', '.') -cne $part -or
            $part -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') { Fail 'INVALID_ARGUMENT' }
        $current = [IO.Path]::Combine($current, $part)
        if ([IO.File]::Exists($current) -or [IO.Directory]::Exists($current)) {
            if (([IO.File]::GetAttributes($current) -band [IO.FileAttributes]::ReparsePoint) -ne 0) { Fail 'BOOTSTRAP_UNSAFE_PATH' }
        }
    }
}
function Relative($Path) {
    if ($Path -isnot [string] -or $Path.Length -eq 0 -or (Length $Path) -gt 2048 -or
        $Path.Contains('\') -or $Path -match '[\x00-\x1f\x7f:<>"|?*]') { Fail 'INVALID_ARGUMENT' }
    foreach ($part in $Path.Split('/')) {
        if ($part -eq '' -or $part -eq '.' -or $part -eq '..' -or $part.TrimEnd(' ', '.') -cne $part -or
            $part -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') { Fail 'INVALID_ARGUMENT' }
    }
}
function Hash([string]$Path) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    $hash = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($hash.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
    finally { $hash.Dispose(); $stream.Dispose() }
}
function Save([string]$Path, $Value, [bool]$Replace = $false) {
    Check $Path
    $temporary = $Path + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($Value | ConvertTo-Json -Compress -Depth 12))
    $stream = [IO.File]::Open($temporary, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($bytes, 0, $bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
    try {
        if ($Replace -and [IO.File]::Exists($Path)) { [IO.File]::Replace($temporary, $Path, $null) }
        else { [IO.File]::Move($temporary, $Path) }
    } finally { if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) } }
}
function Read([string]$Path) {
    Check $Path
    if (-not [IO.File]::Exists($Path) -or ([IO.FileInfo]$Path).Length -gt 131072) { Fail 'BOOTSTRAP_STATE_INVALID' }
    return ([IO.File]::ReadAllText($Path) | ConvertFrom-Json)
}
function VerifyTree([string]$Directory, [string]$ExpectedManifestHash) {
    $manifestPath = Join-Path $Directory '.starbridge-manifest.json'
    $manifest = Read $manifestPath
    if ((Hash $manifestPath) -cne $ExpectedManifestHash) { Fail 'BOOTSTRAP_HASH_MISMATCH' }
    if ($manifest.version -ne 1 -or @($manifest.files).Count -lt 3 -or @($manifest.files).Count -gt 256) { Fail 'BOOTSTRAP_MANIFEST_INVALID' }
    $seen = @{}
    $total = 0L
    foreach ($file in $manifest.files) {
        Relative $file.path
        if ($seen.ContainsKey($file.path) -or $file.sha256 -cnotmatch '^[0-9a-f]{64}$' -or
            ($file.size_bytes -isnot [int] -and $file.size_bytes -isnot [long]) -or $file.size_bytes -lt 0 -or $file.size_bytes -gt 8388608) { Fail 'BOOTSTRAP_MANIFEST_INVALID' }
        $seen[$file.path] = $true
        $path = Join-Path $Directory ($file.path.Replace('/', '\'))
        Check $path
        if (-not [IO.File]::Exists($path) -or ([IO.FileInfo]$path).Length -ne $file.size_bytes -or (Hash $path) -cne $file.sha256) { Fail 'BOOTSTRAP_HASH_MISMATCH' }
        $total += $file.size_bytes
    }
    if ($total -gt 67108864 -or -not $seen.ContainsKey('desktop-bridge.ps1') -or
        -not $seen.ContainsKey('developer-bridge.ps1') -or -not $seen.ContainsKey('developer/system.ps1')) { Fail 'BOOTSTRAP_MANIFEST_INVALID' }
    $queue = [Collections.Generic.Queue[string]]::new()
    $queue.Enqueue($Directory)
    $count = 0
    while ($queue.Count -gt 0) {
        $directoryPath = $queue.Dequeue()
        foreach ($path in [IO.Directory]::EnumerateFileSystemEntries($directoryPath)) {
            Check $path
            if ([IO.Directory]::Exists($path)) { $queue.Enqueue($path) }
            else {
                $relative = $path.Substring($Directory.Length + 1).Replace('\', '/')
                if ($relative -cne '.starbridge-manifest.json' -and -not $seen.ContainsKey($relative)) { Fail 'BOOTSTRAP_MANIFEST_INVALID' }
                $count++
            }
        }
    }
    if ($count -ne ($seen.Count + 1)) { Fail 'BOOTSTRAP_MANIFEST_INVALID' }
    return $seen.Count
}
$mutex = $null
$held = $false
try {
    $target = [string]$r.target_dir
    Check $target
    if ((Length $target) -gt 2048) { Fail 'INVALID_ARGUMENT' }
    $parent = [IO.Path]::GetDirectoryName($target)
    if (-not [IO.Directory]::Exists($parent)) { Fail 'BOOTSTRAP_PARENT_MISSING' }
    $receipt = $target + '.starbridge-receipt.json'
    Check $receipt
    $hasher = [Security.Cryptography.SHA256]::Create()
    try { $lockId = ([BitConverter]::ToString($hasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($target.ToLowerInvariant())))).Replace('-', '') }
    finally { $hasher.Dispose() }
    $mutex = [Threading.Mutex]::new($false, ('Local\Starbridge.Bootstrap.' + $lockId))
    try { $held = $mutex.WaitOne(5000) } catch [Threading.AbandonedMutexException] { $held = $true }
    if (-not $held) { Fail 'BOOTSTRAP_BUSY' }
    if ($r.op -eq 'prepare') {
        if ([IO.File]::Exists($target)) { Fail 'BOOTSTRAP_CONFLICT' }
        if ([IO.Directory]::Exists($target) -and $r.overwrite -ne $true) { Fail 'FILE_EXISTS' }
        $hadTarget = [IO.Directory]::Exists($target)
        $created = 0L; $written = 0L
        $previousManifestHash = ''
        if ($hadTarget) {
            $previousManifestPath = Join-Path $target '.starbridge-manifest.json'
            if (-not [IO.File]::Exists($previousManifestPath)) { Fail 'BOOTSTRAP_UNMANAGED_TARGET' }
            $previousManifestHash = Hash $previousManifestPath
            $null = VerifyTree $target $previousManifestHash
            $created = [IO.Directory]::GetCreationTimeUtc($target).Ticks; $written = [IO.Directory]::GetLastWriteTimeUtc($target).Ticks
        }
        if ([IO.Directory]::Exists($receipt)) { Fail 'BOOTSTRAP_CONFLICT' }
        $hadReceipt = [IO.File]::Exists($receipt)
        $receiptHash = ''
        if ($hadReceipt) {
            if (-not $hadTarget) { Fail 'BOOTSTRAP_CONFLICT' }
            $previousReceipt = Read $receipt
            if ($previousReceipt.installation_id -cnotmatch '^[0-9a-f]{32}$' -or
                $previousReceipt.manifest_sha256 -cne $previousManifestHash -or
                $previousReceipt.backup_available -isnot [bool]) { Fail 'BOOTSTRAP_CONFLICT' }
            $receiptHash = Hash $receipt
        }
        $id = [Guid]::NewGuid().ToString('N')
        $stage = $target + '.starbridge-stage-' + $id
        Check $stage
        [void][IO.Directory]::CreateDirectory($stage)
        [void][IO.Directory]::CreateDirectory((Join-Path $stage 'payload'))
        Save (Join-Path $stage 'control.json') ([ordered]@{installation_id=$id;overwrite=($r.overwrite -eq $true);had_target=$hadTarget;target_created=$created;target_written=$written;previous_manifest_sha256=$previousManifestHash;had_receipt=$hadReceipt;receipt_sha256=$receiptHash})
        $data = @{installation_id=$id}
    } else {
        $id = [string]$r.installation_id
        if ($id -cnotmatch '^[0-9a-f]{32}$') { Fail 'INVALID_ARGUMENT' }
        $stage = $target + '.starbridge-stage-' + $id
        $backup = $target + '.starbridge-backup-' + $id
        $retired = $target + '.starbridge-retired-' + $id
        Check $stage; Check $backup; Check $retired
        if ($r.op -eq 'rollback') {
            $record = Read $receipt
            if ($record.installation_id -cne $id) { Fail 'BOOTSTRAP_CONFLICT' }
            if (-not [IO.Directory]::Exists($backup)) { Fail 'BOOTSTRAP_NO_BACKUP' }
            if (-not [IO.Directory]::Exists($target) -or [IO.Directory]::Exists($retired)) { Fail 'BOOTSTRAP_CONFLICT' }
            $null = VerifyTree $target $record.manifest_sha256
            $null = VerifyTree $backup $record.backup_manifest_sha256
            [IO.Directory]::Move($target, $retired)
            try { [IO.Directory]::Move($backup, $target) }
            catch {
                try { [IO.Directory]::Move($retired, $target) } catch { Fail 'BOOTSTRAP_RECOVERY_REQUIRED' }
                Fail 'BOOTSTRAP_IO'
            }
            # Rollback is complete even if a stale receipt cannot be cleaned up.
            try { [IO.File]::Delete($receipt) } catch { }
            $data = @{installation_id=$id;rolled_back=$true}
        } else {
            $control = Read (Join-Path $stage 'control.json')
            if ($control.installation_id -cne $id) { Fail 'BOOTSTRAP_STATE_INVALID' }
            $payload = Join-Path $stage 'payload'
            Check $payload
            if ($r.op -eq 'put') {
                Relative $r.path
                $path = Join-Path $payload ($r.path.Replace('/', '\'))
                Check $path
                if (($r.offset -isnot [int] -and $r.offset -isnot [long]) -or $r.offset -lt 0 -or $r.offset -gt 8388608) { Fail 'INVALID_ARGUMENT' }
                if ($r.data_base64 -isnot [string] -or $r.data_base64.Length -gt 1368) { Fail 'INVALID_ARGUMENT' }
                $bytes = [Convert]::FromBase64String($r.data_base64)
                if ($bytes.Length -gt 1024 -or [Convert]::ToBase64String($bytes) -cne $r.data_base64) { Fail 'INVALID_ARGUMENT' }
                $directoryPath = [IO.Path]::GetDirectoryName($path)
                [void][IO.Directory]::CreateDirectory($directoryPath)
                Check $directoryPath
                $stream = [IO.File]::Open($path, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::Write, [IO.FileShare]::None)
                try {
                    if ($stream.Length -ne $r.offset -or $stream.Length + $bytes.Length -gt 8388608) { Fail 'BOOTSTRAP_OFFSET' }
                    $stream.Position = $stream.Length
                    $stream.Write($bytes, 0, $bytes.Length)
                    $stream.Flush($true)
                    $next = $stream.Length
                } finally { $stream.Dispose() }
                $data = @{installation_id=$id;next_offset=$next}
            } elseif ($r.op -eq 'commit') {
                $count = VerifyTree $payload $r.manifest_sha256
                $hadTarget = [IO.Directory]::Exists($target)
                if ($hadTarget -ne $control.had_target) { Fail 'BOOTSTRAP_CONFLICT' }
                if ($hadTarget -and ([IO.Directory]::GetCreationTimeUtc($target).Ticks -ne $control.target_created -or
                    [IO.Directory]::GetLastWriteTimeUtc($target).Ticks -ne $control.target_written)) { Fail 'BOOTSTRAP_CONFLICT' }
                if ($hadTarget -and $control.overwrite -ne $true) { Fail 'FILE_EXISTS' }
                if ($hadTarget) { $null = VerifyTree $target $control.previous_manifest_sha256 }
                if ([IO.Directory]::Exists($receipt) -or [IO.File]::Exists($receipt) -ne $control.had_receipt) { Fail 'BOOTSTRAP_CONFLICT' }
                if ($control.had_receipt -and (Hash $receipt) -cne $control.receipt_sha256) { Fail 'BOOTSTRAP_CONFLICT' }
                if ([IO.Directory]::Exists($backup)) { Fail 'BOOTSTRAP_CONFLICT' }
                if ($hadTarget) { [IO.Directory]::Move($target, $backup) }
                try { [IO.Directory]::Move($payload, $target) }
                catch {
                    if ($hadTarget) {
                        try { [IO.Directory]::Move($backup, $target) } catch { Fail 'BOOTSTRAP_RECOVERY_REQUIRED' }
                    }
                    Fail 'BOOTSTRAP_IO'
                }
                try { Save $receipt ([ordered]@{installation_id=$id;manifest_sha256=$r.manifest_sha256;backup_available=$hadTarget;backup_manifest_sha256=$control.previous_manifest_sha256}) ([bool]$control.had_receipt) }
                catch { Fail 'BOOTSTRAP_RECOVERY_REQUIRED' }
                $data = @{installation_id=$id;manifest_sha256=$r.manifest_sha256;file_count=$count;backup_available=$hadTarget}
            } else { Fail 'INVALID_ARGUMENT' }
        }
    }
    @{ok=$true;data=$data} | ConvertTo-Json -Compress -Depth 12
} catch {
    $code = [string]$_.Exception.Message
    if ($code -cnotmatch '^(INVALID_ARGUMENT|FILE_EXISTS|BOOTSTRAP_[A-Z_]+)$') { $code = 'BOOTSTRAP_IO' }
    @{ok=$false;error=@{code=$code;message='Helper installation operation failed; inspect its state before retrying.'}} | ConvertTo-Json -Compress
} finally {
    if ($held) { $mutex.ReleaseMutex() }
    if ($null -ne $mutex) { $mutex.Dispose() }
}
