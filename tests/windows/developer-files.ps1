#requires -Version 5.1
[CmdletBinding()]
param()
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('starbridge-file-contract-' + [Guid]::NewGuid().ToString('N'))
$priorDevHome = $env:STARBRIDGE_DEV_HOME
$powerShellPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$source = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..\guest\windows')).ProviderPath
$null = New-Item -ItemType Directory -Path $fixtureRoot
$environmentHome = Join-Path $fixtureRoot 'configuration'
$workspace = Join-Path $fixtureRoot 'workspace'
$null = New-Item -ItemType Directory -Path $environmentHome,$workspace
$env:STARBRIDGE_DEV_HOME = $environmentHome
$script:FileChecks = 0
function Assert-FileContract([bool]$Value,[string]$Message) { if (!$Value) { throw $Message } }
function Invoke-FileContract([string]$Action,$Parameters,[string]$ErrorCode = '') {
    $request = ConvertTo-Json -InputObject @{action=$Action;params=$Parameters} -Depth 12 -Compress
    $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
    $arguments = @('-NoLogo','-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',(Join-Path $source 'developer-bridge.ps1'),'-RequestBase64',$encoded)
    if ($Action -in @('file.begin','file.write','file.commit','file.abort')) { $arguments += '-AllowWrite' }
    $stderrPath = Join-Path $fixtureRoot 'stderr.txt'
    $output = @(& $powerShellPath @arguments 2> $stderrPath); $exitCode = $LASTEXITCODE
    Assert-FileContract ((Get-Item -LiteralPath $stderrPath).Length -eq 0 -and $output.Count -eq 1) 'Expected a single file protocol response without stderr.'
    $response = ConvertFrom-Json -InputObject ([string]$output[0])
    if ($ErrorCode -eq '') {
        if ($exitCode -ne 0 -or !$response.ok) { throw ('Synthetic file request failed; code={0}.' -f $response.error.code) }
    } else { Assert-FileContract ($exitCode -eq 1 -and !$response.ok -and $response.error.code -ceq $ErrorCode) ('Expected synthetic file error '+$ErrorCode) }
    $script:FileChecks++
    return $response
}
try {
    $configuration = @{version=1;roots=@{workspace=$workspace}} | ConvertTo-Json -Depth 4 -Compress
    [IO.File]::WriteAllText((Join-Path $environmentHome 'files.json'),$configuration,(New-Object Text.UTF8Encoding($false)))
    $bytes = [byte[]]@(0,1,2,3,127,128,254,255)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $hash = [BitConverter]::ToString($sha.ComputeHash($bytes)).Replace('-','').ToLowerInvariant() } finally { $sha.Dispose() }
    $begin = Invoke-FileContract 'file.begin' @{root='workspace';path='nested/synthetic.bin';size_bytes=$bytes.Length;sha256=$hash}
    $id = $begin.data.transfer_id
    Assert-FileContract ($id -cmatch '^[0-9a-f]{32}$' -and $begin.data.offset -eq 0) 'Invalid synthetic transfer identity.'
    $write = Invoke-FileContract 'file.write' @{root='workspace';transfer_id=$id;offset=0;data_base64=[Convert]::ToBase64String($bytes)}
    Assert-FileContract ($write.data.next_offset -eq $bytes.Length) 'Synthetic chunk offset mismatch.'
    $null = Invoke-FileContract 'file.write' @{root='workspace';transfer_id=$id;offset=0;data_base64='AA=='} 'TRANSFER_OFFSET_MISMATCH'
    $commit = Invoke-FileContract 'file.commit' @{root='workspace';transfer_id=$id}
    Assert-FileContract ($commit.data.sha256 -ceq $hash -and $commit.data.size_bytes -eq $bytes.Length) 'Committed synthetic bytes failed checksum.'
    $info = Invoke-FileContract 'file.hash' @{root='workspace';path='nested/synthetic.bin'}
    Assert-FileContract ($info.data.sha256 -ceq $hash) 'Synthetic hash mismatch.'
    $read = Invoke-FileContract 'file.read' @{root='workspace';path='nested/synthetic.bin';offset=2;limit=3;expected_size=$bytes.Length;expected_version=$info.data.version}
    Assert-FileContract ($read.data.offset -eq 2 -and $read.data.next_offset -eq 5 -and !$read.data.eof -and $read.data.data_base64 -ceq [Convert]::ToBase64String($bytes,2,3)) 'Synthetic read chunk is incorrect.'
    $null = Invoke-FileContract 'file.read' @{root='workspace';path='nested/synthetic.bin';expected_size=$bytes.Length;expected_version='synthetic-stale-version'} 'FILE_CHANGED'
    $null = Invoke-FileContract 'file.begin' @{root='workspace';path='nested/synthetic.bin';size_bytes=$bytes.Length;sha256=$hash} 'FILE_EXISTS'
    $null = Invoke-FileContract 'file.hash' @{root='workspace';path='missing.bin'} 'FILE_NOT_FOUND'
    foreach ($path in @('../escape','/absolute','a//b','a/./b','a/../b','NUL.txt','a.','a ','a:b','.starbridge-transfers/state')) {
        $null = Invoke-FileContract 'file.hash' @{root='workspace';path=$path} 'INVALID_PATH'
    }
    $null = Invoke-FileContract 'file.hash' @{root='unconfigured';path='synthetic.bin'} 'ROOT_NOT_ALLOWED'
    $begin = Invoke-FileContract 'file.begin' @{root='workspace';path='incomplete.bin';size_bytes=1;sha256=$hash}
    $null = Invoke-FileContract 'file.commit' @{root='workspace';transfer_id=$begin.data.transfer_id} 'TRANSFER_INCOMPLETE'
    $null = Invoke-FileContract 'file.abort' @{root='workspace';transfer_id=$begin.data.transfer_id}
    $null = Invoke-FileContract 'file.abort' @{root='workspace';transfer_id=$begin.data.transfer_id}
    $outside = Join-Path $fixtureRoot 'outside'; $null = New-Item -ItemType Directory -Path $outside
    $junction = Join-Path $workspace 'junction'
    $null = New-Item -ItemType Junction -Path $junction -Target $outside
    try { $null = Invoke-FileContract 'file.begin' @{root='workspace';path='junction/escape.bin';size_bytes=0;sha256=('0'*64)} 'INVALID_PATH' }
    finally { [IO.Directory]::Delete($junction) }
    Write-Host ('Synthetic file contracts passed ({0} checks; no user files inspected).' -f $script:FileChecks)
} finally {
    $env:STARBRIDGE_DEV_HOME = $priorDevHome
    if (Test-Path -LiteralPath $fixtureRoot) { Remove-Item -LiteralPath $fixtureRoot -Recurse -Force }
}
