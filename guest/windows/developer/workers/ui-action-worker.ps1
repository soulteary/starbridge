#requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$RequestBase64,[switch]$AllowWrite)
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';$WarningPreference='SilentlyContinue'
[Console]::OutputEncoding=New-Object Text.UTF8Encoding($false)
$script:DevUiErrorCode='UI_OPERATION_FAILED';$script:DevUiErrorMessage='The UI operation could not complete; inspect application state before retrying.'
$script:DevUiMutationBegan=$false
. (Join-Path $PSScriptRoot 'ui-core.ps1')
. (Join-Path $PSScriptRoot 'ui-input-native.ps1')
. (Join-Path $PSScriptRoot 'ui-action-core.ps1')
try {
    if(!$AllowWrite){Stop-DevUi 'WRITE_NOT_ALLOWED' 'The isolated UI worker requires explicit AllowWrite authorization.'}
    if($RequestBase64.Length -gt 24000){Stop-DevUi 'INVALID_ARGUMENT' 'The UI request is too large.'}
    $bytes=[Convert]::FromBase64String($RequestBase64)
    if($bytes.Length -gt 16000){Stop-DevUi 'INVALID_ARGUMENT' 'The UI request is too large.'}
    $utf8=New-Object Text.UTF8Encoding($false,$true);$text=$utf8.GetString($bytes)
    if(!$text.TrimStart().StartsWith('{')){Stop-DevUi 'INVALID_ARGUMENT' 'The UI request must be a JSON object.'}
    $request=ConvertFrom-Json -InputObject $text -ErrorAction Stop
    Assert-DevUiFields $request @('action','params')
    $name=Get-DevUiString $request 'action' '' 32
    $p=Resolve-DevUiActionParameters $name (Get-DevUiField $request 'params')
    $data=Invoke-DevUiActionCore $name $p
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject ([ordered]@{ok=$true;data=$data}) -Depth 16 -Compress))
    exit 0
}catch{
    $outcome='not_started';if($script:DevUiMutationBegan){$outcome='unknown'}
    [Console]::Out.WriteLine((ConvertTo-Json -InputObject ([ordered]@{ok=$false;error=[ordered]@{code=$script:DevUiErrorCode;message=$script:DevUiErrorMessage;details=[ordered]@{outcome=$outcome}}}) -Depth 8 -Compress))
    exit 1
}
