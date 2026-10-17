#requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$RequestBase64)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
$script:DevUiErrorCode = 'UIA_UNAVAILABLE'
$script:DevUiErrorMessage = 'UI inspection could not complete in this desktop session.'
. (Join-Path $PSScriptRoot 'ui-core.ps1')
try {
    if ($RequestBase64.Length -gt 24000) { Stop-DevUi 'INVALID_ARGUMENT' 'The UI request is too large.' }
    $bytes = [Convert]::FromBase64String($RequestBase64)
    if ($bytes.Length -gt 16000) { Stop-DevUi 'INVALID_ARGUMENT' 'The UI request is too large.' }
    $utf8 = New-Object Text.UTF8Encoding($false, $true)
    $json = $utf8.GetString($bytes)
    if (!$json.TrimStart().StartsWith('{')) { Stop-DevUi 'INVALID_ARGUMENT' 'The UI request must be a JSON object.' }
    $p = Resolve-DevUiQueryParameters (ConvertFrom-Json -InputObject $json -ErrorAction Stop)
    $data = Invoke-DevUiQueryCore $p
    $output = ConvertTo-Json -InputObject ([ordered]@{ok=$true;data=$data}) -Depth 24 -Compress
    if ([Text.Encoding]::UTF8.GetByteCount($output) -gt 4194304) { Stop-DevUi 'OUTPUT_LIMIT_EXCEEDED' 'The UI query exceeded its output budget.' }
    [Console]::Out.WriteLine($output)
    exit 0
} catch {
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject ([ordered]@{ok=$false;error=[ordered]@{code=$script:DevUiErrorCode;message=$script:DevUiErrorMessage}}) -Compress))
    exit 1
}
