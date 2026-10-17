#requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$RequestBase64)
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';$WarningPreference='SilentlyContinue'
[Console]::OutputEncoding=New-Object Text.UTF8Encoding($false)
$script:DevUiErrorCode='SCREENSHOT_PROBE_FAILED';$script:DevUiErrorMessage='Screenshot capability probing failed.'
. (Join-Path $PSScriptRoot 'ui-core.ps1')
. (Join-Path $PSScriptRoot 'screenshot-native.ps1')
try{
    if($RequestBase64.Length -gt 24000){Stop-DevUi 'INVALID_ARGUMENT' 'The capability request is too large.'}
    $bytes=[Convert]::FromBase64String($RequestBase64)
    if($bytes.Length -gt 16000){Stop-DevUi 'INVALID_ARGUMENT' 'The capability request is too large.'}
    $text=(New-Object Text.UTF8Encoding($false,$true)).GetString($bytes)
    if(!$text.TrimStart().StartsWith('{')){Stop-DevUi 'INVALID_ARGUMENT' 'The capability request must be a JSON object.'}
    $request=ConvertFrom-Json -InputObject $text -ErrorAction Stop
    Assert-DevUiFields $request @('timeout_ms')
    $null=Get-DevUiInteger $request 'timeout_ms' 10000 1000 20000
    Initialize-DevScreenshotNative
    $probe=[StarbridgeDeveloperScreenshot.Wgc]::Probe()
    $process=[Diagnostics.Process]::GetCurrentProcess()
    try{$session=$process.SessionId}finally{$process.Dispose()}
    $data=[ordered]@{probe_scope='os-api-device';os_build=$probe.os_build;session_id=$session;user_interactive=[Environment]::UserInteractive;capture_requires_input_desktop=$true;methods=@(
        [ordered]@{capture_method='print_window';available=$true;reason='available'},
        [ordered]@{capture_method='windows_graphics_capture';available=$probe.available;reason=$probe.reason}
    )}
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject @{ok=$true;data=$data} -Depth 8 -Compress));exit 0
}catch{
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject @{ok=$false;error=@{code=$script:DevUiErrorCode;message=$script:DevUiErrorMessage}} -Depth 8 -Compress));exit 1
}
