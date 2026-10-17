#requires -Version 5.1
<#
Run with Windows PowerShell 5.1 from any directory:
  powershell.exe -NoProfile -ExecutionPolicy Bypass -File tests\windows\guest-smoke.ps1
Tests the real guest process without Parallels, an interactive desktop, or UIA.
#>
[CmdletBinding()]
param(
    [string]$HelperPath = (Join-Path $PSScriptRoot '..\..\guest\windows\desktop-bridge.ps1')
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$script:Checks = 0

function Assert-Contract([bool]$Condition, [string]$Message) {
    if (!$Condition) { throw $Message }
    $script:Checks++
}

function Encode-Request($Request) {
    $json = ConvertTo-Json -InputObject $Request -Depth 8 -Compress
    return [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
}

function Invoke-Helper([string]$EncodedRequest) {
    # The child is essential: it verifies stdout and the actual exit code, and
    # prevents the helper's exit statement from terminating the test runner.
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $script:PowerShellPath
    $startInfo.Arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' +
        $script:ResolvedHelperPath + '" -RequestBase64 "' + $EncodedRequest + '"'
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.StandardOutputEncoding = New-Object Text.UTF8Encoding($false, $true)
    $startInfo.StandardErrorEncoding = New-Object Text.UTF8Encoding($false, $true)
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $startInfo
    $stdout = '<not read>'
    try {
        Assert-Contract ($process.Start()) 'The guest helper did not start.'
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (!$process.WaitForExit(30000)) {
            try { $process.Kill(); $null = $process.WaitForExit(2000) } catch { }
            throw 'The guest helper exceeded the 30 second smoke-test deadline.'
        }
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        Assert-Contract (![string]::IsNullOrWhiteSpace($stdout)) 'The guest helper returned no JSON.'
        Assert-Contract ($stdout.TrimStart().StartsWith('{')) 'stdout must contain one JSON object.'
        try { $response = ConvertFrom-Json -InputObject $stdout -ErrorAction Stop } catch {
            throw 'stdout contains invalid JSON or text outside its JSON response.'
        }
        Assert-Contract ($response -is [PSCustomObject]) 'The JSON response must be an object.'
        Assert-Contract ($response.ok -is [bool]) 'The response ok field must be a boolean.'
        Assert-Contract ([string]::IsNullOrWhiteSpace($stderr)) 'The helper wrote unexpected diagnostics to stderr.'
        return [PSCustomObject]@{ response=$response; exit_code=$process.ExitCode }
    } catch {
        $exitCode = '<not exited>'
        try { $exitCode = $process.ExitCode } catch { }
        throw ('{0}; exit_code={1}; response={2}' -f $_.Exception.Message, $exitCode, $stdout)
    } finally { $process.Dispose() }
}

function Assert-Success($Result, [string]$Action) {
    Assert-Contract ($Result.exit_code -eq 0) "$Action must exit with code 0."
    Assert-Contract ($Result.response.ok -eq $true) "$Action must report ok=true."
    Assert-Contract ($null -ne $Result.response.PSObject.Properties['data']) 'Success must contain data.'
    Assert-Contract ($null -ne $Result.response.PSObject.Properties['meta']) 'Success must contain meta.'
    $meta = $Result.response.meta
    Assert-Contract (($meta.protocol_version -is [string]) -and ($meta.protocol_version -ceq '1')) 'Wrong protocol version.'
    Assert-Contract (($meta.action -is [string]) -and ($meta.action -ceq $Action)) 'Metadata action must match the request.'
    Assert-Contract (($meta.duration_ms -is [int] -or $meta.duration_ms -is [long]) -and ($meta.duration_ms -ge 0)) 'duration_ms must be a nonnegative integer.'
    Assert-Contract (($meta.session_id -is [int] -or $meta.session_id -is [long]) -and ($meta.session_id -ge 0)) 'session_id must be a nonnegative integer.'
    Assert-Contract ($meta.user_interactive -is [bool]) 'user_interactive must be a boolean.'
    Assert-Contract (($meta.read_only -is [bool]) -and $meta.read_only) 'Queries must identify themselves as read_only.'
    $timestamp = [DateTimeOffset]::MinValue
    Assert-Contract (($meta.timestamp -is [string]) -and [DateTimeOffset]::TryParse($meta.timestamp, [ref]$timestamp)) 'timestamp must be a parseable timestamp.'
}

function Assert-Failure($Result, [string]$Code) {
    Assert-Contract ($Result.exit_code -eq 1) 'A rejected request must exit with code 1.'
    Assert-Contract ($Result.response.ok -eq $false) 'A rejected request must report ok=false.'
    Assert-Contract ($null -ne $Result.response.PSObject.Properties['error']) 'A failure must contain error.'
    Assert-Contract ($Result.response.error.code -ceq $Code) "Expected error code $Code."
    Assert-Contract (($Result.response.error.message -is [string]) -and ![string]::IsNullOrWhiteSpace($Result.response.error.message)) 'A failure must contain a nonempty message.'
}

function Assert-ProcessList($Result, [int]$Limit) {
    Assert-Success $Result 'processes.list'
    $data = $Result.response.data
    Assert-Contract ($data.processes -is [array]) 'processes must remain a JSON array, including zero and one item.'
    Assert-Contract ($data.processes.Count -le $Limit) 'The process list exceeded the requested limit.'
    Assert-Contract ($data.limit -eq $Limit) 'The response must echo the requested limit.'
    Assert-Contract ($data.truncated -is [bool]) 'truncated must be a boolean.'
    foreach ($item in $data.processes) {
        Assert-Contract (($item.pid -is [int] -or $item.pid -is [long]) -and ($item.pid -ge 0)) 'A process PID must be a nonnegative integer.'
        Assert-Contract (($item.name -is [string]) -and ![string]::IsNullOrWhiteSpace($item.name)) 'A process must have a name.'
    }
}

try {
    $script:ResolvedHelperPath = (Resolve-Path -LiteralPath $HelperPath).ProviderPath
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($script:ResolvedHelperPath, [ref]$tokens, [ref]$parseErrors)
    Assert-Contract ($parseErrors.Count -eq 0) ('Guest helper syntax errors: ' + (($parseErrors | ForEach-Object { $_.Message }) -join '; '))
    Write-Host 'PASS: guest helper parses with Windows PowerShell.'

    $script:PowerShellPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) {
        $script:PowerShellPath = Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
    }
    Assert-Contract (Test-Path -LiteralPath $script:PowerShellPath -PathType Leaf) 'Windows PowerShell 5.1 is required.'

    $limited = Invoke-Helper (Encode-Request @{ action='processes.list'; params=@{ limit=1 } })
    Assert-ProcessList $limited 1
    Write-Host 'PASS: limited process query returns a typed JSON envelope.'

    # The runner stays alive while the child enumerates it, making a singleton
    # array deterministic without depending on a particular installed program.
    $single = Invoke-Helper (Encode-Request @{ action='processes.list'; params=@{ pid=$PID; limit=1; include_path=$true } })
    Assert-ProcessList $single 1
    Assert-Contract ($single.response.data.processes.Count -eq 1) 'PID query must find the smoke-test process.'
    $item = $single.response.data.processes[0]
    Assert-Contract ($item.pid -eq $PID) 'PID filtering returned a different process.'
    Assert-Contract ($item.path_status -in @('available', 'unavailable')) 'include_path must report its availability.'
    if ($item.path_status -ceq 'available') {
        Assert-Contract (($item.path -is [string]) -and ![string]::IsNullOrWhiteSpace($item.path)) 'An available process path must be a nonempty string.'
    }
    Write-Host 'PASS: singleton arrays, PID filtering, and optional process paths.'

    $absentName = 'starbridge-smoke-' + [Guid]::NewGuid().ToString('N')
    $empty = Invoke-Helper (Encode-Request @{ action='processes.list'; params=@{ name=$absentName; limit=2 } })
    Assert-ProcessList $empty 2
    Assert-Contract ($empty.response.data.processes.Count -eq 0) 'A missing process must return an empty JSON array.'
    Assert-Contract (!$empty.response.data.truncated) 'An empty result must not be truncated.'
    Write-Host 'PASS: empty process arrays and name filtering.'

    $rejectedCases = @(
        @{ name='invalid-base64'; encoded='@@@'; code='INVALID_REQUEST' },
        @{ name='invalid-utf8'; encoded=[Convert]::ToBase64String([byte[]]@(0xC3, 0x28)); code='INVALID_REQUEST' },
        @{ name='incomplete-json'; json='{'; code='INVALID_REQUEST' },
        @{ name='request-empty-array'; json='[]'; code='INVALID_REQUEST' },
        @{ name='request-singleton-array'; json='[{"action":"processes.list","params":{}}]'; code='INVALID_REQUEST' },
        @{ name='request-null'; json='null'; code='INVALID_REQUEST' },
        @{ name='request-scalar'; json='true'; code='INVALID_REQUEST' },
        @{ name='unknown-action'; json='{"action":"not.supported","params":{}}'; code='UNKNOWN_ACTION' },
        @{ name='action-singleton-array'; json='{"action":["processes.list"],"params":{}}'; code='UNKNOWN_ACTION' },
        @{ name='params-empty-array'; json='{"action":"processes.list","params":[]}'; code='INVALID_REQUEST' },
        @{ name='params-singleton-array'; json='{"action":"processes.list","params":[{}]}'; code='INVALID_REQUEST' },
        @{ name='params-null'; json='{"action":"processes.list","params":null}'; code='INVALID_REQUEST' },
        @{ name='params-scalar'; json='{"action":"processes.list","params":false}'; code='INVALID_REQUEST' },
        @{ name='limit-zero'; json='{"action":"processes.list","params":{"limit":0}}'; code='INVALID_REQUEST' },
        @{ name='limit-too-large'; json='{"action":"processes.list","params":{"limit":1001}}'; code='INVALID_REQUEST' },
        @{ name='limit-boolean'; json='{"action":"processes.list","params":{"limit":true}}'; code='INVALID_REQUEST' },
        @{ name='limit-empty-array'; json='{"action":"processes.list","params":{"limit":[]}}'; code='INVALID_REQUEST' },
        @{ name='limit-singleton-array'; json='{"action":"processes.list","params":{"limit":[1]}}'; code='INVALID_REQUEST' },
        @{ name='pid-negative'; json='{"action":"processes.list","params":{"pid":-1}}'; code='INVALID_REQUEST' },
        @{ name='pid-singleton-array'; json='{"action":"processes.list","params":{"pid":[1]}}'; code='INVALID_REQUEST' },
        @{ name='name-number'; json='{"action":"processes.list","params":{"name":123}}'; code='INVALID_REQUEST' },
        @{ name='name-singleton-array'; json='{"action":"processes.list","params":{"name":["notepad"]}}'; code='INVALID_REQUEST' },
        @{ name='include-path-string'; json='{"action":"processes.list","params":{"include_path":"true"}}'; code='INVALID_REQUEST' },
        @{ name='include-path-singleton-array'; json='{"action":"processes.list","params":{"include_path":[true]}}'; code='INVALID_REQUEST' }
    )
    foreach ($case in $rejectedCases) {
        $result = $null
        try {
            $encoded = $case['encoded']
            if ($case.ContainsKey('json')) { $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($case.json)) }
            $result = Invoke-Helper $encoded
            Assert-Failure $result $case.code
        } catch {
            $exitCode = '<not returned>'
            $response = '<not returned>'
            if ($null -ne $result) {
                $exitCode = $result.exit_code
                $response = ConvertTo-Json -InputObject $result.response -Depth 8 -Compress
            }
            throw ("Rejected case '{0}' failed: {1}; exit_code={2}; response={3}" -f $case.name, $_.Exception.Message, $exitCode, $response)
        }
        Write-Host ('PASS: rejected case ' + $case.name)
    }
    Write-Host 'PASS: malformed input and invalid parameter bounds/types fail as JSON with exit code 1.'

    # CI may have no interactive desktop. A valid empty list or an explicit
    # session error both satisfy this smoke test; UIA is intentionally untested.
    $windows = Invoke-Helper (Encode-Request @{ action='windows.list'; params=@{ limit=1 } })
    if (!$windows.response.ok) {
        Assert-Failure $windows 'INTERACTIVE_SESSION_REQUIRED'
        Write-Host 'PASS: windows.list explicitly reports that an interactive session is required.'
    } else {
        Assert-Success $windows 'windows.list'
        $data = $windows.response.data
        Assert-Contract ($data.windows -is [array]) 'windows must be a JSON array.'
        Assert-Contract ($data.windows.Count -le 1) 'The window list exceeded its limit.'
        Assert-Contract ($data.limit -eq 1) 'The window list must echo its requested limit.'
        Assert-Contract ($data.truncated -is [bool]) 'Window truncation must be a boolean.'
        Assert-Contract ($data.coordinate_space -in @('guest-screen-physical-pixels', 'guest-screen-dpi-context-dependent-pixels')) 'Unexpected window coordinate space.'
        Write-Host 'PASS: windows.list basic contract without requiring a visible window.'
    }

    Write-Host ('Guest smoke test passed ({0} assertions). UIA was not exercised.' -f $script:Checks)
    exit 0
} catch {
    [Console]::Error.WriteLine('Guest smoke test failed: ' + $_.Exception.Message)
    exit 1
}
