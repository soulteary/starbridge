# Root aliases are configured locally; requests never choose an absolute guest path.
function Assert-DevFileChain([string]$Path) {
    $current = [IO.Path]::GetFullPath($Path)
    while (![string]::IsNullOrEmpty($current)) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -Force -LiteralPath $current
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { Stop-Dev 'INVALID_PATH' 'Reparse points are not permitted in file roots or transfers.' }
        }
        $parent = [IO.Path]::GetDirectoryName($current.TrimEnd([char[]]'\/'))
        if ($parent -eq $current) { break }; $current = $parent
    }
}

function Get-DevFileRelative([string]$Path) {
    if ([string]::IsNullOrEmpty($Path) -or (Get-DevCodePointLength $Path) -gt 2048 -or $Path -match '[\x00-\x1f\x7f:<>"|?*]') { Stop-Dev 'INVALID_PATH' 'Expected a bounded relative file path.' }
    $value = $Path.Replace('\','/')
    foreach ($part in $value.Split('/')) {
        if ($part -eq '' -or $part -eq '.' -or $part -eq '..' -or $part -match '[. ]$' -or $part -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)' -or $part -ieq '.starbridge-transfers') {
            Stop-Dev 'INVALID_PATH' 'The relative path contains a reserved or unsafe component.'
        }
    }
    return $value
}

function Get-DevFileRoot([string]$Alias) {
    if ($Alias -cnotmatch '^[A-Za-z][A-Za-z0-9_-]{0,63}$') { Stop-Dev 'INVALID_FILE_ROOT' 'Invalid file root alias.' }
    $homePath = $env:STARBRIDGE_DEV_HOME
    if ([string]::IsNullOrEmpty($homePath)) { $homePath = Join-Path $env:LOCALAPPDATA 'Starbridge\Developer' }
    $configuration = Join-Path $homePath 'files.json'
    Assert-DevFileChain $configuration
    if (!(Test-Path -LiteralPath $configuration -PathType Leaf) -or (Get-Item -LiteralPath $configuration).Length -gt 65536) { Stop-Dev 'INVALID_FILE_ROOT' 'Install a bounded local files.json configuration.' }
    try { $config = ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($configuration)) } catch { Stop-Dev 'INVALID_FILE_ROOT' 'Invalid file root configuration.' }
    Assert-DevFields $config @('version','roots')
    if ((Get-DevInteger $config 'version' 0 1 1) -ne 1 -or !(Test-DevObject $config.roots)) { Stop-Dev 'INVALID_FILE_ROOT' 'Expected version 1 root configuration.' }
    $property = @($config.roots.PSObject.Properties | Where-Object { $_.Name -ceq $Alias })
    if ($property.Count -ne 1 -or $property[0].Value -isnot [string]) { Stop-Dev 'ROOT_NOT_ALLOWED' 'The file root alias is not configured.' }
    $path = $property[0].Value
    if ($path -notmatch '^[A-Za-z]:[\\/]') { Stop-Dev 'INVALID_FILE_ROOT' 'File roots must use absolute local drive paths.' }
    $path = [IO.Path]::GetFullPath($path).TrimEnd([char[]]'\/') + '\'
    Assert-DevFileChain $path
    if (!(Test-Path -LiteralPath $path -PathType Container)) { Stop-Dev 'INVALID_FILE_ROOT' 'Configured file root directory does not exist.' }
    return $path
}

function Get-DevFileTarget([string]$Root,[string]$Relative) {
    $relative = Get-DevFileRelative $Relative
    $target = [IO.Path]::GetFullPath((Join-Path $Root $relative.Replace('/','\')))
    if (!$target.StartsWith($Root,[StringComparison]::OrdinalIgnoreCase)) { Stop-Dev 'INVALID_PATH' 'The file path leaves its configured root.' }
    Assert-DevFileChain $target
    return $target
}

function Get-DevFileVersion([string]$Path) {
    $item = Get-Item -Force -LiteralPath $Path
    return ('{0}-{1}-{2}' -f $item.Length,$item.LastWriteTimeUtc.Ticks,$item.CreationTimeUtc.Ticks)
}

function Get-DevFileDigest([string]$Path) {
    $stream = [IO.File]::Open($Path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-','').ToLowerInvariant() }
    finally { $sha.Dispose(); $stream.Dispose() }
}

function Get-DevFileStage([string]$Root,[string]$Id) {
    if ($Id -cnotmatch '^[0-9a-f]{32}$') { Stop-Dev 'INVALID_REQUEST' 'Invalid transfer identity.' }
    $directory = Join-Path $Root '.starbridge-transfers'
    Assert-DevFileChain $directory
    return @{ directory=$directory; metadata=(Join-Path $directory ($Id+'.json')); content=(Join-Path $directory ($Id+'.bin')) }
}

function Save-DevFileStage($Paths,$State) {
    Assert-DevFileChain $Paths.metadata
    $temporary = $Paths.metadata + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    [IO.File]::WriteAllText($temporary,(ConvertTo-Json -InputObject $State -Depth 8 -Compress),(New-Object Text.UTF8Encoding($false)))
    try {
        if ([IO.File]::Exists($Paths.metadata)) { [IO.File]::Replace($temporary,$Paths.metadata,$null) }
        else { [IO.File]::Move($temporary,$Paths.metadata) }
    } finally { if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) } }
}

function Read-DevFileStage($Paths,[string]$Alias) {
    Assert-DevFileChain $Paths.metadata; Assert-DevFileChain $Paths.content
    if (!(Test-Path -LiteralPath $Paths.metadata -PathType Leaf) -or (Get-Item -LiteralPath $Paths.metadata).Length -gt 8192) { Stop-Dev 'TRANSFER_NOT_FOUND' 'The transfer identity is unavailable.' }
    try { $state = ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($Paths.metadata)) } catch { Stop-Dev 'INVALID_TRANSFER' 'Invalid transfer state.' }
    Assert-DevFields $state @('root','path','size_bytes','sha256','overwrite','offset','destination_version')
    if ($state.root -cne $Alias -or $state.sha256 -cnotmatch '^[0-9a-f]{64}$') { Stop-Dev 'INVALID_TRANSFER' 'Transfer identity does not match its root.' }
    $null = Get-DevFileRelative (Get-DevString $state 'path' $null 2048 -Required)
    $null = Get-DevInteger $state 'size_bytes' 0 0 1073741824
    $null = Get-DevInteger $state 'offset' 0 0 1073741824
    $null = Get-DevBoolean $state 'overwrite' $false
    return $state
}

function Invoke-DevFile([string]$Action,$p) {
    $allowed = @('root','path')
    switch ($Action) {
        'file.read' { $allowed += @('offset','limit','expected_size','expected_version') }
        'file.begin' { $allowed += @('size_bytes','sha256','overwrite') }
        'file.write' { $allowed = @('root','transfer_id','offset','data_base64') }
        'file.commit' { $allowed = @('root','transfer_id') }
        'file.abort' { $allowed = @('root','transfer_id') }
    }
    Assert-DevFields $p $allowed
    $alias = Get-DevString $p 'root' $null 64 -Required
    $rootPath = Get-DevFileRoot $alias
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $lockHash = [BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($rootPath.ToLowerInvariant()))).Replace('-','') } finally { $sha.Dispose() }
    $mutex = New-Object Threading.Mutex($false,('Local\Starbridge.Files.'+$lockHash))
    $held = $false
    try {
        try { $held = $mutex.WaitOne(5000) } catch [Threading.AbandonedMutexException] { $held = $true }
        if (!$held) { Stop-Dev 'FILE_BUSY' 'Another file operation is active for this root.' }
        if ($Action -in @('file.hash','file.read','file.begin')) {
            $relative = Get-DevFileRelative (Get-DevString $p 'path' $null 2048 -Required)
            $target = Get-DevFileTarget $rootPath $relative
        }
        if ($Action -eq 'file.hash') {
            if (!(Test-Path -LiteralPath $target -PathType Leaf)) { Stop-Dev 'FILE_NOT_FOUND' 'The requested file does not exist.' }
            $size = (Get-Item -LiteralPath $target).Length
            if ($size -gt 1073741824) { Stop-Dev 'FILE_TOO_LARGE' 'The file exceeds 1 GiB.' }
            $version = Get-DevFileVersion $target; $hash = Get-DevFileDigest $target
            if ((Get-DevFileVersion $target) -cne $version) { Stop-Dev 'FILE_CHANGED' 'The file changed while hashing.' }
            return [ordered]@{root=$alias;path=$relative;size_bytes=[long]$size;sha256=$hash;version=$version}
        }
        if ($Action -eq 'file.read') {
            $offset = Get-DevInteger $p 'offset' 0 0 1073741824; $limit = Get-DevInteger $p 'limit' 65536 1 65536
            if ($null -eq $p.PSObject.Properties['expected_size']) { Stop-Dev 'INVALID_REQUEST' 'expected_size is required.' }
            $size = Get-DevInteger $p 'expected_size' 0 0 1073741824
            $version = Get-DevString $p 'expected_version' $null 256 -Required
            if (!(Test-Path -LiteralPath $target -PathType Leaf)) { Stop-Dev 'FILE_NOT_FOUND' 'The requested file does not exist.' }
            if ((Get-Item -LiteralPath $target).Length -ne $size -or (Get-DevFileVersion $target) -cne $version -or $offset -gt $size) { Stop-Dev 'FILE_CHANGED' 'The requested file version is no longer available.' }
            $stream = [IO.File]::Open($target,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
            try { $null = $stream.Seek($offset,[IO.SeekOrigin]::Begin); $buffer = New-Object byte[] ([int][Math]::Min($limit,$size-$offset)); $read = $stream.Read($buffer,0,$buffer.Length) }
            finally { $stream.Dispose() }
            if ((Get-DevFileVersion $target) -cne $version -or ($read -eq 0 -and $offset -lt $size)) { Stop-Dev 'FILE_CHANGED' 'The file changed while reading.' }
            $encoded = [Convert]::ToBase64String($buffer,0,$read)
            return [ordered]@{root=$alias;path=$relative;size_bytes=$size;version=$version;offset=$offset;next_offset=($offset+$read);data_base64=$encoded;eof=($offset+$read -eq $size)}
        }
        if ($Action -eq 'file.begin') {
            if ($null -eq $p.PSObject.Properties['size_bytes']) { Stop-Dev 'INVALID_REQUEST' 'size_bytes is required.' }
            $size = Get-DevInteger $p 'size_bytes' 0 0 1073741824
            $hash = Get-DevString $p 'sha256' $null 64 -Required
            if ($hash -cnotmatch '^[0-9a-f]{64}$') { Stop-Dev 'INVALID_REQUEST' 'sha256 must be lowercase hexadecimal.' }
            $overwrite = Get-DevBoolean $p 'overwrite' $false
            $destinationVersion = $null
            if (Test-Path -LiteralPath $target) {
                if (!$overwrite -or !(Test-Path -LiteralPath $target -PathType Leaf)) { Stop-Dev 'FILE_EXISTS' 'The target already exists.' }
                $destinationVersion = Get-DevFileVersion $target
            }
            $parent = [IO.Path]::GetDirectoryName($target); $null = [IO.Directory]::CreateDirectory($parent); Assert-DevFileChain $parent
            $id = [Guid]::NewGuid().ToString('N'); $paths = Get-DevFileStage $rootPath $id
            $null = [IO.Directory]::CreateDirectory($paths.directory); Assert-DevFileChain $paths.directory
            if (@(Get-ChildItem -LiteralPath $paths.directory -Filter '*.json' -File).Count -ge 128) { Stop-Dev 'TRANSFER_LIMIT' 'Abort incomplete transfers before creating more.' }
            $stream = [IO.File]::Open($paths.content,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None); $stream.Dispose()
            $state = [ordered]@{root=$alias;path=$relative;size_bytes=$size;sha256=$hash;overwrite=$overwrite;offset=[long]0;destination_version=$destinationVersion}
            Save-DevFileStage $paths $state
            return [ordered]@{root=$alias;path=$relative;transfer_id=$id;offset=[long]0}
        }
        $id = Get-DevString $p 'transfer_id' $null 32 -Required; $paths = Get-DevFileStage $rootPath $id
        if ($Action -eq 'file.abort') {
            foreach ($path in @($paths.content,$paths.metadata)) { Assert-DevFileChain $path; if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) } }
            return [ordered]@{root=$alias;transfer_id=$id;aborted=$true}
        }
        $state = Read-DevFileStage $paths $alias
        if ($Action -eq 'file.write') {
            $offset = Get-DevInteger $p 'offset' 0 0 1073741824
            $encoded = Get-DevString $p 'data_base64' $null 8192 -Required
            try { $bytes = [Convert]::FromBase64String($encoded) } catch { Stop-Dev 'INVALID_REQUEST' 'Invalid transfer bytes.' }
            if ($bytes.Length -eq 0 -or $bytes.Length -gt 6144 -or [Convert]::ToBase64String($bytes) -cne $encoded) { Stop-Dev 'INVALID_REQUEST' 'Expected a canonical, bounded nonempty Base64 chunk.' }
            if ($offset -ne $state.offset -or $offset+$bytes.Length -gt $state.size_bytes -or (Get-Item -LiteralPath $paths.content).Length -ne $offset) { Stop-Dev 'TRANSFER_OFFSET_MISMATCH' 'The chunk does not match the durable transfer offset.' }
            $stream = [IO.File]::Open($paths.content,[IO.FileMode]::Open,[IO.FileAccess]::Write,[IO.FileShare]::None)
            try { $null = $stream.Seek($offset,[IO.SeekOrigin]::Begin); $stream.Write($bytes,0,$bytes.Length); $stream.Flush($true) } finally { $stream.Dispose() }
            $state.offset = [long]($offset+$bytes.Length); Save-DevFileStage $paths $state
            return [ordered]@{root=$alias;transfer_id=$id;next_offset=$state.offset}
        }
        if ($Action -eq 'file.commit') {
            if ($state.offset -ne $state.size_bytes -or (Get-Item -LiteralPath $paths.content).Length -ne $state.size_bytes) { Stop-Dev 'TRANSFER_INCOMPLETE' 'Transfer has not received all expected bytes.' }
            if ((Get-DevFileDigest $paths.content) -cne $state.sha256) { Stop-Dev 'CHECKSUM_MISMATCH' 'Transfer contents do not match their declared SHA-256.' }
            $target = Get-DevFileTarget $rootPath $state.path
            $present = Test-Path -LiteralPath $target
            if ($present) {
                if (!$state.overwrite) { Stop-Dev 'FILE_EXISTS' 'The target already exists.' }
                if ($null -eq $state.destination_version -or (Get-DevFileVersion $target) -cne $state.destination_version) { Stop-Dev 'FILE_CHANGED' 'The destination changed after transfer preparation.' }
                [IO.File]::Replace($paths.content,$target,$null)
            } else {
                if ($null -ne $state.destination_version) { Stop-Dev 'FILE_CHANGED' 'The destination disappeared after transfer preparation.' }
                [IO.File]::Move($paths.content,$target)
            }
            # Publishing has completed. A cleanup failure must not report an ambiguous failed commit.
            try { [IO.File]::Delete($paths.metadata) } catch { }
            return [ordered]@{root=$alias;path=$state.path;transfer_id=$id;size_bytes=$state.size_bytes;sha256=$state.sha256}
        }
        Stop-Dev 'UNKNOWN_ACTION' 'Unknown file action.'
    } finally { if ($held) { $mutex.ReleaseMutex() }; $mutex.Dispose() }
}

Register-DeveloperAction -Name 'file.hash' -Handler { param($p); Invoke-DevFile 'file.hash' $p }
Register-DeveloperAction -Name 'file.read' -Handler { param($p); Invoke-DevFile 'file.read' $p }
Register-DeveloperAction -Name 'file.begin' -Mutation:$true -Handler { param($p); Invoke-DevFile 'file.begin' $p }
Register-DeveloperAction -Name 'file.write' -Mutation:$true -Handler { param($p); Invoke-DevFile 'file.write' $p }
Register-DeveloperAction -Name 'file.commit' -Mutation:$true -Handler { param($p); Invoke-DevFile 'file.commit' $p }
Register-DeveloperAction -Name 'file.abort' -Mutation:$true -Handler { param($p); Invoke-DevFile 'file.abort' $p }
