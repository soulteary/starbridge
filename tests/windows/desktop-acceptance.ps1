#requires -Version 5.1
<# Opt-in positive UIA acceptance. Requires a visible logged-in test desktop.
   Starts and closes only its own fixture; never operates on an existing app. #>
[CmdletBinding()]
param([string]$HelperPath = (Join-Path $PSScriptRoot '..\..\guest\windows\desktop-bridge.ps1'))
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$helper = (Resolve-Path -LiteralPath $HelperPath).ProviderPath
$powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) {
    $powershell = Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
}
function Assert-Desktop([bool]$Condition,[string]$Message) {
    if (!$Condition) { throw $Message }
}
function Invoke-Query([string]$Action,$Parameters) {
    $request = ConvertTo-Json -InputObject @{action=$Action;params=$Parameters} -Depth 8 -Compress
    $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $powershell
    $start.Arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' + $helper + '" -RequestBase64 ' + $encoded
    $start.UseShellExecute = $false
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.StandardOutputEncoding = New-Object Text.UTF8Encoding($false,$true)
    $start.StandardErrorEncoding = New-Object Text.UTF8Encoding($false,$true)
    $child = New-Object Diagnostics.Process
    $child.StartInfo = $start
    try {
        $null = $child.Start()
        $outputTask = $child.StandardOutput.ReadToEndAsync()
        $errorTask = $child.StandardError.ReadToEndAsync()
        if (!$child.WaitForExit(30000)) {
            try { $child.Kill(); $null = $child.WaitForExit(2000) } catch { }
            throw "$Action exceeded the acceptance deadline."
        }
        $output = $outputTask.GetAwaiter().GetResult()
        $errors = $errorTask.GetAwaiter().GetResult()
        Assert-Desktop ($child.ExitCode -eq 0) "$Action failed; raw output and diagnostics withheld."
        try { $response = ConvertFrom-Json -InputObject $output -ErrorAction Stop }
        catch { throw "$Action returned invalid JSON; raw response withheld." }
        Assert-Desktop ($response.ok -eq $true) "$Action did not return success; raw response withheld."
        return $response
    } finally { $child.Dispose() }
}
$temp = Join-Path ([IO.Path]::GetTempPath()) ('starbridge-desktop-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $temp
$metadataPath = Join-Path $temp 'fixture.json'
$fixturePath = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot 'desktop-fixture.ps1')).ProviderPath
$fixture = $null
try {
    $command = "& '" + $fixturePath.Replace("'","''") + "' -MetadataPath '" + $metadataPath.Replace("'","''") + "'"
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $fixture = Start-Process -FilePath $powershell -ArgumentList @('-NoProfile','-Sta','-ExecutionPolicy','Bypass','-EncodedCommand',$encoded) -PassThru
    $timer = [Diagnostics.Stopwatch]::StartNew()
    while (!(Test-Path -LiteralPath $metadataPath)) {
        if ($fixture.HasExited -or $timer.ElapsedMilliseconds -gt 15000) { throw 'Visible fixture did not start; use a logged-in interactive desktop.' }
        Start-Sleep -Milliseconds 100
    }
    $metadata = ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($metadataPath))
    $processes = Invoke-Query 'processes.list' @{pid=[int]$metadata.pid;limit=1}
    Assert-Desktop ($processes.data.processes.Count -eq 1) 'Fixture PID was not found.'
    Assert-Desktop ($processes.data.processes[0].pid -eq $metadata.pid) 'Wrong process was returned.'
    $windows = Invoke-Query 'windows.list' @{pid=[int]$metadata.pid;limit=10}
    $matched = @($windows.data.windows | Where-Object { $_.hwnd -eq $metadata.hwnd })
    Assert-Desktop ($matched.Count -eq 1) 'The known fixture HWND was not found.'
    Assert-Desktop ($matched[0].title -eq $metadata.title) 'The fixture title did not match.'
    Assert-Desktop (($matched[0].rect.width -gt 0) -and ($matched[0].rect.height -gt 0)) 'The fixture has no usable rectangle.'
    $tree = Invoke-Query 'ui.tree' @{hwnd=$metadata.hwnd;max_depth=4;max_nodes=50;timeout_ms=20000}
    $buttons = @($tree.data.nodes | Where-Object { $_.name -eq $metadata.button_name -and $_.control_type -eq 'Button' })
    Assert-Desktop ($buttons.Count -eq 1) 'The known Unicode button was not found in UIA.'
    Assert-Desktop ($buttons[0].is_enabled -eq $true) 'The fixture button is unexpectedly disabled.'
    $root = Invoke-Query 'ui.tree' @{hwnd=$metadata.hwnd;max_depth=0;max_nodes=1;timeout_ms=20000}
    Assert-Desktop ($root.data.nodes.Count -eq 1) 'Root-only query must contain exactly one node.'
    Assert-Desktop ($root.data.nodes[0].depth -eq 0) 'Root depth must be zero.'
    Write-Host 'PASS: known PID, HWND, title, rectangle, Unicode button and bounded UIA root.'
    Write-Host ('Fixture session: ' + $tree.meta.session_id)
} finally {
    try {
        if ($null -ne $fixture) {
            try {
                if (!$fixture.HasExited) {
                    $null = $fixture.CloseMainWindow()
                    if (!$fixture.WaitForExit(3000)) { $fixture.Kill(); $null = $fixture.WaitForExit(2000) }
                }
            } catch [InvalidOperationException] {
                # The fixture can exit between HasExited and CloseMainWindow.
            } finally { $fixture.Dispose() }
        }
    } finally { Remove-Item -LiteralPath $temp -Recurse -Force }
}
