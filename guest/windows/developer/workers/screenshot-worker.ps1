#requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$RequestBase64)
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';$WarningPreference='SilentlyContinue'
[Console]::OutputEncoding=New-Object Text.UTF8Encoding($false)
$script:DevUiErrorCode='SCREENSHOT_FAILED';$script:DevUiErrorMessage='The requested window rendering could not be captured.'
. (Join-Path $PSScriptRoot 'ui-core.ps1')
. (Join-Path $PSScriptRoot 'ui-input-native.ps1')
. (Join-Path $PSScriptRoot 'screenshot-native.ps1')
. (Join-Path $PSScriptRoot 'screenshot-core.ps1')
try {
    if($RequestBase64.Length -gt 24000){Stop-DevUi 'INVALID_ARGUMENT' 'The capture request is too large.'}
    $bytes=[Convert]::FromBase64String($RequestBase64)
    if($bytes.Length -gt 16000){Stop-DevUi 'INVALID_ARGUMENT' 'The capture request is too large.'}
    $utf8=New-Object Text.UTF8Encoding($false,$true);$text=$utf8.GetString($bytes)
    if(!$text.TrimStart().StartsWith('{')){Stop-DevUi 'INVALID_ARGUMENT' 'The capture request must be a JSON object.'}
    $request=ConvertFrom-Json -InputObject $text -ErrorAction Stop
    $parameters=Resolve-DevScreenshotParameters $request
    $data=Invoke-DevScreenshotCore $parameters
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject ([ordered]@{ok=$true;data=$data}) -Depth 12 -Compress))
    exit 0
} catch {
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject ([ordered]@{ok=$false;error=[ordered]@{code=$script:DevUiErrorCode;message=$script:DevUiErrorMessage}}) -Depth 8 -Compress))
    exit 1
}
