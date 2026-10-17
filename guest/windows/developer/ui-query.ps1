# UI query registration. The worker is isolated so a blocked UIA provider has a
# hard process deadline. This module never loads or changes the legacy helper.
function Invoke-DevUiWorker([string]$WorkerName, $Parameters, [int]$TimeoutMs, [bool]$Mutation = $false) {
    if ($WorkerName -notmatch '^[a-z-]+\.ps1$') { Stop-Dev 'INTERNAL_ERROR' 'Invalid UI worker name.' }
    $workerPath = Join-Path $script:DeveloperRoot ('developer\workers\' + $WorkerName)
    if (!(Test-Path -LiteralPath $workerPath -PathType Leaf)) { Stop-Dev 'UIA_WORKER_UNAVAILABLE' 'Install the complete developer helper directory.' }
    $powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) {
        $powerShell = Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
    }
    $json = ConvertTo-Json -InputObject $Parameters -Depth 12 -Compress
    $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
    $command = "& '" + $workerPath.Replace("'", "''") + "' -RequestBase64 '" + $encoded + "'"
    if ($Mutation) { $command += ' -AllowWrite' }
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $powerShell
    $start.Arguments = '-NoLogo -NoProfile -NonInteractive -Mta -ExecutionPolicy Bypass -EncodedCommand ' + [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.StandardOutputEncoding = New-Object Text.UTF8Encoding($false)
    $start.StandardErrorEncoding = New-Object Text.UTF8Encoding($false)
    $child = New-Object Diagnostics.Process
    $child.StartInfo = $start
    try {
        if (!$child.Start()) { Stop-Dev 'UIA_WORKER_FAILED' 'The UI worker could not start.' }
        $stdout = $child.StandardOutput.ReadToEndAsync()
        $stderr = $child.StandardError.ReadToEndAsync()
        if (!$child.WaitForExit($TimeoutMs)) {
            try { $child.Kill(); $null = $child.WaitForExit(2000) } catch { }
            if ($Mutation) { Stop-Dev 'ACTION_TIMEOUT' 'The UI action outcome is unknown after worker timeout. Inspect application state before retrying.' }
            Stop-Dev 'UIA_TIMEOUT' 'UI inspection exceeded timeout_ms; worker termination was attempted.'
        }
        $output = $stdout.GetAwaiter().GetResult()
        $null = $stderr.GetAwaiter().GetResult()
        if ([Text.Encoding]::UTF8.GetByteCount($output) -gt 4194304) { Stop-Dev 'OUTPUT_LIMIT_EXCEEDED' 'The UI worker exceeded its output budget.' }
        try {
            if (!$output.TrimStart().StartsWith('{')) { throw 'Invalid envelope.' }
            $response = ConvertFrom-Json -InputObject $output -ErrorAction Stop
            if ($response.ok -isnot [bool]) { throw 'Invalid envelope.' }
        } catch { Stop-Dev 'UIA_WORKER_FAILED' 'The UI worker returned an invalid response.' }
        if (!$response.ok) {
            if (($response.error.code -isnot [string]) -or ($response.error.message -isnot [string]) -or
                $response.error.code.Length -gt 80 -or $response.error.message.Length -gt 512) {
                Stop-Dev 'UIA_WORKER_FAILED' 'The UI worker returned an invalid error.'
            }
            Stop-Dev $response.error.code $response.error.message
        }
        if ($child.ExitCode -ne 0) { Stop-Dev 'UIA_WORKER_FAILED' 'The UI worker reported success with a failing exit code.' }
        return $response.data
    } finally { $child.Dispose() }
}

Register-DeveloperAction -Name 'ui.query' -Description 'Search controls within an identified window; values are opt-in and password values are skipped.' -Mutation:$false -Interactive:$true -Handler {
    param($p)
    $timeout = Get-DevInteger $p 'timeout_ms' 10000 1000 20000
    Invoke-DevUiWorker 'ui-query-worker.ps1' $p $timeout
}
