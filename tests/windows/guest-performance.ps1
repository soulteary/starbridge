#requires -Version 5.1
<#
Run in Windows PowerShell 5.1. No VM is started and no application is launched.
  .\tests\windows\guest-performance.ps1 -OutputPath .\guest-performance.csv
For UIA, supply a fresh handle from windows.list in the same interactive session:
  .\tests\windows\guest-performance.ps1 -Hwnd 0x12345 -Iterations 5
wall_ms includes a fresh PowerShell process and helper output. helper_ms comes
from response metadata (the UIA worker's duration for ui.tree). startup-only
measures PowerShell startup/output without the helper. Compare raw samples on
the same machine and window; this script sets no speedup or pass/fail threshold.
#>
[CmdletBinding()]
param(
    [string]$HelperPath = (Join-Path $PSScriptRoot '..\..\guest\windows\desktop-bridge.ps1'),
    [ValidateRange(1, 100)][int]$Iterations = 5,
    [string]$Hwnd = '',
    [int[]]$NodeLimits = @(1, 25, 100, 250, 1000),
    [ValidateRange(0, 12)][int]$MaxDepth = 6,
    [ValidateRange(1000, 20000)][int]$TimeoutMs = 10000,
    [string]$OutputPath = ''
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Run this script on Windows.' }
foreach ($limit in $NodeLimits) {
    if ($limit -lt 1 -or $limit -gt 1000) { throw 'NodeLimits must contain integers in 1..1000.' }
}
$script:ResolvedHelperPath = (Resolve-Path -LiteralPath $HelperPath).ProviderPath
$script:HelperHash = (Get-FileHash -LiteralPath $script:ResolvedHelperPath -Algorithm SHA256).Hash
$script:PowerShellPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) {
    $script:PowerShellPath = Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
}
$script:Rows = New-Object 'System.Collections.Generic.List[object]'

function Invoke-SampleProcess([string]$Arguments) {
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $script:PowerShellPath
    $startInfo.Arguments = $Arguments
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.StandardOutputEncoding = New-Object Text.UTF8Encoding($false, $true)
    $startInfo.StandardErrorEncoding = New-Object Text.UTF8Encoding($false, $true)
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $startInfo
    $timer = [Diagnostics.Stopwatch]::StartNew()
    try {
        if (!$process.Start()) { throw 'The sample process did not start.' }
        $outputTask = $process.StandardOutput.ReadToEndAsync()
        $errorTask = $process.StandardError.ReadToEndAsync()
        if (!$process.WaitForExit(30000)) {
            try { $process.Kill(); $null = $process.WaitForExit(2000) } catch { }
            throw 'The sample process exceeded 30 seconds; inspect guest processes before repeating.'
        }
        $stdout = $outputTask.GetAwaiter().GetResult()
        $stderr = $errorTask.GetAwaiter().GetResult()
        $timer.Stop()
        if (![string]::IsNullOrWhiteSpace($stderr)) { throw ('Unexpected sample stderr: ' + $stderr) }
        $response = ConvertFrom-Json -InputObject $stdout -ErrorAction Stop
        if ($response.ok -isnot [bool]) { throw 'The sample returned an invalid response envelope.' }
        if (($response.ok -and $process.ExitCode -ne 0) -or (!$response.ok -and $process.ExitCode -ne 1)) {
            throw 'The sample response and process exit code disagree.'
        }
        return [PSCustomObject]@{
            response = $response
            exit_code = $process.ExitCode
            wall_ms = [Math]::Round($timer.Elapsed.TotalMilliseconds, 3)
            response_bytes = [Text.Encoding]::UTF8.GetByteCount($stdout)
        }
    } finally { $timer.Stop(); $process.Dispose() }
}

function Add-Sample([string]$Name, [int]$Iteration, $Request, [int]$NodeLimit = 0) {
    $arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass '
    if ($null -eq $Request) {
        $startupCommand = '[Console]::Out.WriteLine(''{"ok":true,"data":{}}'')'
        $arguments += '-EncodedCommand ' + [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($startupCommand))
    } else {
        $json = ConvertTo-Json -InputObject $Request -Depth 8 -Compress
        $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
        $arguments += '-File "' + $script:ResolvedHelperPath + '" -RequestBase64 ' + $encoded
    }
    $sample = Invoke-SampleProcess $arguments
    $response = $sample.response
    $helperMs = $null
    $sessionId = $null
    if ($null -ne $response.PSObject.Properties['meta']) {
        $helperMs = $response.meta.duration_ms
        $sessionId = $response.meta.session_id
    }
    $count = $null
    $truncated = $null
    $errorCode = $null
    if (!$response.ok) { $errorCode = $response.error.code }
    elseif ($Name -like 'processes-*') {
        if ($response.data.processes -isnot [array]) { throw 'processes must remain a JSON array.' }
        $count = $response.data.processes.Count
        $truncated = $response.data.truncated
    } elseif ($Name -eq 'ui-tree') {
        if ($response.data.nodes -isnot [array]) { throw 'nodes must remain a JSON array.' }
        $count = $response.data.nodes.Count
        $truncated = $response.data.node_limit_reached
        if ($count -lt 1 -or $count -gt $NodeLimit) { throw 'UIA node count is outside the requested bound.' }
        if ($response.data.nodes[0].parent_id -ne $null -or $response.data.nodes[0].depth -ne 0) {
            throw 'The UIA tree must begin with its requested window root.'
        }
    }
    $script:Rows.Add([PSCustomObject][ordered]@{
        case = $Name; iteration = $Iteration; node_limit = $NodeLimit
        max_depth = $MaxDepth; timeout_ms = $TimeoutMs
        wall_ms = $sample.wall_ms; helper_ms = $helperMs
        observed_count = $count; truncated = $truncated
        ok = $response.ok; error_code = $errorCode; exit_code = $sample.exit_code
        response_bytes = $sample.response_bytes; session_id = $sessionId
        hwnd = $Hwnd; helper_sha256 = $script:HelperHash
    })
    return $response
}

for ($iteration = 1; $iteration -le $Iterations; $iteration++) {
    $null = Add-Sample 'startup-only' $iteration $null
    $all = Add-Sample 'processes-all' $iteration @{ action='processes.list'; params=@{ limit=1000 } }
    if (!$all.ok) { throw 'The unfiltered process query failed.' }
    $target = Add-Sample 'processes-pid' $iteration @{ action='processes.list'; params=@{ pid=$PID; limit=1 } }
    if (!$target.ok -or $target.data.processes.Count -ne 1 -or $target.data.processes[0].pid -ne $PID -or $target.data.truncated) {
        throw 'The targeted PID query must return exactly the live benchmark process.'
    }
    if ($Hwnd) {
        foreach ($limit in $NodeLimits) {
            $null = Add-Sample 'ui-tree' $iteration @{ action='ui.tree'; params=@{
                hwnd=$Hwnd; max_nodes=$limit; max_depth=$MaxDepth; timeout_ms=$TimeoutMs
            } } $limit
        }
    }
}

# Also protect empty-result semantics of the direct PID path and combined filters.
$missingPid = [int]::MaxValue
$knownProcesses = [Diagnostics.Process]::GetProcesses()
try { while ($knownProcesses.Id -contains $missingPid) { $missingPid-- } }
finally { foreach ($process in $knownProcesses) { $process.Dispose() } }
$missing = Add-Sample 'processes-missing-pid' 1 @{ action='processes.list'; params=@{ pid=$missingPid; limit=1 } }
$filtered = Add-Sample 'processes-pid-name-mismatch' 1 @{ action='processes.list'; params=@{ pid=$PID; name=[Guid]::NewGuid().ToString('N') } }
foreach ($empty in @($missing, $filtered)) {
    if (!$empty.ok -or $empty.data.processes.Count -ne 0 -or $empty.data.truncated) {
        throw 'Missing PIDs and mismatched names must return untruncated empty arrays.'
    }
}
if (!$Hwnd) { Write-Host 'UIA samples skipped: supply -Hwnd from the current interactive desktop to measure tree queries.' }
if ($OutputPath) {
    $script:Rows.ToArray() | Export-Csv -LiteralPath $OutputPath -NoTypeInformation -Encoding UTF8
    Write-Host ('Saved raw timing samples to ' + $OutputPath)
}
$script:Rows.ToArray()
