# Synthetic directory installation checks; no VM, real installation or user file is used.
$ErrorActionPreference = 'Stop'
$loader = [IO.File]::ReadAllText((Join-Path $PSScriptRoot '..\..\internal\bridge\developer_bootstrap.ps1'))
$tokens = $null; $parseErrors = $null
$null = [Management.Automation.Language.Parser]::ParseInput($loader, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) { throw ('Bootstrap loader parse errors: ' + ($parseErrors.Message -join '; ')) }
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('starbridge-bootstrap-test-' + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($fixture)
function Assert-Bootstrap($Condition, [string]$Message) { if (-not $Condition) { throw ('Bootstrap fixture assertion: ' + $Message) } }
function Invoke-Loader($Request) {
    $json = $Request | ConvertTo-Json -Depth 12 -Compress
    $request64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
    $memory = [IO.MemoryStream]::new()
    $zip = [IO.Compression.GZipStream]::new($memory, [IO.Compression.CompressionMode]::Compress, $true)
    $loaderBytes = [Text.Encoding]::UTF8.GetBytes($loader)
    $zip.Write($loaderBytes, 0, $loaderBytes.Length)
    $zip.Dispose()
    $loader64 = [Convert]::ToBase64String($memory.ToArray())
    $memory.Dispose()
    $code = '$r=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(''' + $request64 + '''))|ConvertFrom-Json;' +
        '$m=[IO.MemoryStream]::new([Convert]::FromBase64String(''' + $loader64 + '''),$false);' +
        '$g=[IO.Compression.GZipStream]::new($m,[IO.Compression.CompressionMode]::Decompress);$s=[IO.StreamReader]::new($g,[Text.Encoding]::UTF8);try{$l=$s.ReadToEnd()}finally{$s.Dispose()}; & ([ScriptBlock]::Create($l))'
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
    Assert-Bootstrap ($encoded.Length -le 30000) 'encoded command budget'
    $output = & powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand $encoded 2>$null
    Assert-Bootstrap ($LASTEXITCODE -eq 0) 'loader process exited'
    try { return (($output -join "`n") | ConvertFrom-Json) } catch { throw 'Bootstrap fixture: invalid loader JSON.' }
}
function Get-FixtureHash([byte[]]$Bytes) {
    $hash = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($hash.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant() } finally { $hash.Dispose() }
}
function Install-Fixture([string]$Target, [string]$Label, [bool]$Overwrite, $BeforeCommit=$null, [string]$ExpectedCode='') {
    $prepare = Invoke-Loader @{op='prepare';target_dir=$Target;overwrite=$Overwrite}
    Assert-Bootstrap ($prepare.ok -eq $true) 'prepare failed'
    $id = $prepare.data.installation_id
    $files = @()
    foreach ($path in @('desktop-bridge.ps1', 'developer-bridge.ps1', 'developer/system.ps1', 'developer/workers/fixture.cs')) {
        $bytes = [Text.Encoding]::UTF8.GetBytes(('# synthetic ' + $Label + ' ' + $path))
        $put = Invoke-Loader @{op='put';target_dir=$Target;installation_id=$id;path=$path;offset=0;data_base64=[Convert]::ToBase64String($bytes)}
        Assert-Bootstrap ($put.ok -eq $true -and $put.data.next_offset -eq $bytes.Length) 'put failed'
        $files += @{path=$path;size_bytes=$bytes.Length;sha256=(Get-FixtureHash $bytes)}
    }
    $manifest = [Text.Encoding]::UTF8.GetBytes((@{version=1;files=$files} | ConvertTo-Json -Compress -Depth 8))
    for ($offset=0; $offset -lt $manifest.Length; $offset += 1024) {
        $count = [Math]::Min(1024, $manifest.Length - $offset)
        $chunk = New-Object byte[] $count
        [Array]::Copy($manifest, $offset, $chunk, 0, $count)
        $put = Invoke-Loader @{op='put';target_dir=$Target;installation_id=$id;path='.starbridge-manifest.json';offset=$offset;data_base64=[Convert]::ToBase64String($chunk)}
        Assert-Bootstrap ($put.ok -eq $true) 'manifest put failed'
    }
    if ($null -ne $BeforeCommit) { & $BeforeCommit }
    $commit = Invoke-Loader @{op='commit';target_dir=$Target;installation_id=$id;manifest_sha256=(Get-FixtureHash $manifest)}
    if ($ExpectedCode -ne '') {
        Assert-Bootstrap ($commit.ok -eq $false -and $commit.error.code -ceq $ExpectedCode) 'expected commit conflict'
        return $commit
    }
    Assert-Bootstrap ($commit.ok -eq $true -and $commit.data.file_count -eq 4) 'commit failed'
    return $commit.data
}
try {
    $unmanaged = Join-Path $fixture 'unmanaged'
    [void][IO.Directory]::CreateDirectory($unmanaged)
    $unmanagedFile = Join-Path $unmanaged 'synthetic.txt'
    [IO.File]::WriteAllText($unmanagedFile, 'Synthetic unrelated content')
    $unmanagedResult = Invoke-Loader @{op='prepare';target_dir=$unmanaged;overwrite=$true}
    Assert-Bootstrap ($unmanagedResult.ok -eq $false -and $unmanagedResult.error.code -eq 'BOOTSTRAP_UNMANAGED_TARGET') 'unmanaged directory accepted'
    Assert-Bootstrap ([IO.File]::ReadAllText($unmanagedFile) -ceq 'Synthetic unrelated content') 'unmanaged directory changed'
    $collisionTarget = Join-Path $fixture 'receipt-collision'
    $collisionReceipt = $collisionTarget + '.starbridge-receipt.json'
    [IO.File]::WriteAllText($collisionReceipt, 'Synthetic unrelated receipt')
    $collision = Invoke-Loader @{op='prepare';target_dir=$collisionTarget;overwrite=$false}
    Assert-Bootstrap ($collision.ok -eq $false -and $collision.error.code -eq 'BOOTSTRAP_CONFLICT') 'unrelated receipt accepted'
    Assert-Bootstrap ([IO.File]::ReadAllText($collisionReceipt) -ceq 'Synthetic unrelated receipt') 'unrelated receipt overwritten'
    $target = Join-Path $fixture 'helper'
    $first = Install-Fixture $target 'first' $false
    Assert-Bootstrap ($first.backup_available -eq $false) 'first installation has no backup'
    $denied = Invoke-Loader @{op='prepare';target_dir=$target;overwrite=$false}
    Assert-Bootstrap ($denied.ok -eq $false -and $denied.error.code -eq 'FILE_EXISTS') 'overwrite must be explicit'
    $receiptPath = $target + '.starbridge-receipt.json'
    $originalReceipt = [IO.File]::ReadAllBytes($receiptPath)
    $null = Install-Fixture $target 'receipt-change' $true {
        [IO.File]::AppendAllText($receiptPath, ' ')
    } 'BOOTSTRAP_CONFLICT'
    Assert-Bootstrap ([IO.File]::ReadAllText((Join-Path $target 'desktop-bridge.ps1')).Contains('first')) 'receipt conflict moved current installation'
    [IO.File]::WriteAllBytes($receiptPath, $originalReceipt)
    $second = Install-Fixture $target 'second' $true
    Assert-Bootstrap ($second.backup_available -eq $true) 'update retains backup'
    $backupFile = Join-Path ($target + '.starbridge-backup-' + $second.installation_id) 'desktop-bridge.ps1'
    $backupOriginal = [IO.File]::ReadAllBytes($backupFile)
    [IO.File]::WriteAllText($backupFile, '# synthetic damaged backup')
    $damagedBackup = Invoke-Loader @{op='rollback';target_dir=$target;installation_id=$second.installation_id}
    Assert-Bootstrap ($damagedBackup.ok -eq $false -and $damagedBackup.error.code -eq 'BOOTSTRAP_HASH_MISMATCH') 'damaged backup accepted'
    Assert-Bootstrap ([IO.File]::ReadAllText((Join-Path $target 'desktop-bridge.ps1')).Contains('second')) 'damaged rollback moved current installation'
    [IO.File]::WriteAllBytes($backupFile, $backupOriginal)
    $wrong = Invoke-Loader @{op='rollback';target_dir=$target;installation_id=('f' * 32)}
    Assert-Bootstrap ($wrong.ok -eq $false -and $wrong.error.code -eq 'BOOTSTRAP_CONFLICT') 'rollback identity mismatch'
    $extra = Join-Path $target 'unexpected.ps1'
    [IO.File]::WriteAllText($extra, '# synthetic extra')
    $changed = Invoke-Loader @{op='rollback';target_dir=$target;installation_id=$second.installation_id}
    Assert-Bootstrap ($changed.ok -eq $false -and $changed.error.code -eq 'BOOTSTRAP_MANIFEST_INVALID') 'changed installation must not be silently replaced'
    [IO.File]::Delete($extra)
    $rollback = Invoke-Loader @{op='rollback';target_dir=$target;installation_id=$second.installation_id}
    Assert-Bootstrap ($rollback.ok -eq $true -and $rollback.data.rolled_back -eq $true) 'rollback failed'
    Assert-Bootstrap ([IO.File]::ReadAllText((Join-Path $target 'desktop-bridge.ps1')).Contains('first')) 'previous helper restored'

    $prepare = Invoke-Loader @{op='prepare';target_dir=(Join-Path $fixture 'bad');overwrite=$false}
    $id = $prepare.data.installation_id
    foreach ($path in @('../escape.ps1', 'a/../../escape', 'C:/absolute', 'a//b', 'NUL.txt', 'trailing.')) {
        $bad = Invoke-Loader @{op='put';target_dir=(Join-Path $fixture 'bad');installation_id=$id;path=$path;offset=0;data_base64='YQ=='}
        Assert-Bootstrap ($bad.ok -eq $false -and $bad.error.code -eq 'INVALID_ARGUMENT') 'unsafe relative path accepted'
    }
    $put = Invoke-Loader @{op='put';target_dir=(Join-Path $fixture 'bad');installation_id=$id;path='safe.ps1';offset=0;data_base64='YQ=='}
    Assert-Bootstrap ($put.ok -eq $true) 'initial offset'
    $again = Invoke-Loader @{op='put';target_dir=(Join-Path $fixture 'bad');installation_id=$id;path='safe.ps1';offset=0;data_base64='Yg=='}
    Assert-Bootstrap ($again.ok -eq $false -and $again.error.code -eq 'BOOTSTRAP_OFFSET') 'duplicate chunk accepted'
    $commit = Invoke-Loader @{op='commit';target_dir=(Join-Path $fixture 'bad');installation_id=$id;manifest_sha256=('0' * 64)}
    Assert-Bootstrap ($commit.ok -eq $false) 'incomplete helper committed'
    Assert-Bootstrap (-not [IO.Directory]::Exists((Join-Path $fixture 'bad'))) 'incomplete install replaced destination'

    # New-Item junction creation needs no administrator privilege on Windows.
    $junction = Join-Path $fixture 'junction'
    $outside = Join-Path $fixture 'outside'
    [void][IO.Directory]::CreateDirectory($outside)
    try {
        New-Item -ItemType Junction -Path $junction -Target $outside -ErrorAction Stop | Out-Null
        $unsafe = Invoke-Loader @{op='prepare';target_dir=(Join-Path $junction 'helper');overwrite=$false}
        Assert-Bootstrap ($unsafe.ok -eq $false -and $unsafe.error.code -eq 'BOOTSTRAP_UNSAFE_PATH') 'reparse parent accepted'
    } finally {
        if ([IO.Directory]::Exists($junction)) { [IO.Directory]::Delete($junction) }
    }
    'Developer bootstrap synthetic checks passed.'
} finally {
    if ([IO.Directory]::Exists($fixture)) { [IO.Directory]::Delete($fixture, $true) }
}
exit 0
