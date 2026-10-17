#requires -Version 5.1
<#
Developer protocol dev-1. Modules return data; only this entry writes envelopes.
Install developer-bridge.ps1 and the complete adjacent developer directory.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RequestBase64,
    [switch]$AllowWrite
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
$script:DeveloperRoot = $PSScriptRoot
$script:DeveloperScriptPath = $PSCommandPath
$script:DevStartedAt = [DateTime]::UtcNow
$script:DevFailureCode = 'INTERNAL_ERROR'
$script:DevFailureMessage = 'The developer helper could not complete the request.'
$script:DevActionName = ''
$script:DevMutation = $false
$script:DevActions = New-Object 'Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)

function Stop-Dev([string]$Code, [string]$Message) {
    $script:DevFailureCode = $Code
    $script:DevFailureMessage = $Message
    throw $Message
}

function Test-DevObject($Value) {
    return (($null -ne $Value) -and ($Value.GetType() -eq [Management.Automation.PSCustomObject]))
}

function Assert-DevFields($Object, [string[]]$Allowed) {
    if (!(Test-DevObject $Object)) { Stop-Dev 'INVALID_REQUEST' 'Parameters must be a JSON object.' }
    foreach ($property in $Object.PSObject.Properties) {
        if ($Allowed -cnotcontains $property.Name) { Stop-Dev 'INVALID_REQUEST' ('Unknown field: ' + $property.Name) }
    }
}

function Get-DevField($Object, [string]$Name, $Default = $null) {
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return ,$Default }
    # Preserve JSON [] and [one] as arrays rather than PowerShell pipeline values.
    return ,$property.Value
}

function Get-DevCodePointLength([string]$Text) {
    $count = 0
    for ($index = 0; $index -lt $Text.Length; $index++) {
        if ([char]::IsSurrogatePair($Text, $index)) { $index++ }
        $count++
    }
    return $count
}

function Get-DevString($Object, [string]$Name, $Default = $null, [int]$Max = 512, [switch]$Required) {
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) {
        if ($Required) { Stop-Dev 'INVALID_REQUEST' ($Name + ' is required.') }
        return ,$Default
    }
    $value = $property.Value
    if (($value -isnot [string]) -or ((Get-DevCodePointLength $value) -gt $Max) -or ($Required -and $value.Length -eq 0)) {
        Stop-Dev 'INVALID_REQUEST' ($Name + ' must be a bounded string.')
    }
    return $value
}

function Get-DevInteger($Object, [string]$Name, [long]$Default, [long]$Min, [long]$Max) {
    $value = Get-DevField $Object $Name $Default
    $numeric = ($value -is [int]) -or ($value -is [long]) -or ($value -is [double]) -or ($value -is [decimal])
    if (!$numeric -or ($value -lt $Min) -or ($value -gt $Max)) {
        Stop-Dev 'INVALID_REQUEST' ($Name + ' must be an integer within its permitted range.')
    }
    if (($value -is [double]) -and ([double]::IsNaN($value) -or [double]::IsInfinity($value) -or
        [Math]::Abs($value) -gt 9007199254740991 -or [Math]::Truncate($value) -ne $value)) {
        Stop-Dev 'INVALID_REQUEST' ($Name + ' must be an exact integer.')
    }
    if (($value -is [decimal]) -and ([decimal]::Truncate($value) -ne $value)) { Stop-Dev 'INVALID_REQUEST' ($Name + ' must be an integer.') }
    return [long]$value
}

function Get-DevBoolean($Object, [string]$Name, [bool]$Default) {
    $value = Get-DevField $Object $Name $Default
    if ($value -isnot [bool]) { Stop-Dev 'INVALID_REQUEST' ($Name + ' must be a boolean.') }
    return $value
}

function Register-DeveloperAction {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Handler,
        [bool]$Mutation = $false,
        [bool]$Interactive = $false,
        [string]$Description = ''
    )
    if (($Name -cnotmatch '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$') -or $script:DevActions.ContainsKey($Name)) {
        Stop-Dev 'INVALID_MODULE' 'The developer action registration is invalid or duplicated.'
    }
    if ($script:DevActions.Count -ge 256) { Stop-Dev 'INVALID_MODULE' 'Too many developer actions are installed.' }
    $script:DevActions.Add($Name, [PSCustomObject]@{ handler=$Handler; mutation=$Mutation; interactive=$Interactive; description=$Description })
}

function New-DevMetadata {
    return [ordered]@{
        protocol_version = 'dev-1'
        action = $script:DevActionName
        timestamp = [DateTime]::UtcNow.ToString('o')
        duration_ms = [long]([DateTime]::UtcNow - $script:DevStartedAt).TotalMilliseconds
        session_id = [Diagnostics.Process]::GetCurrentProcess().SessionId
        user_interactive = [Environment]::UserInteractive
        read_only = !$script:DevMutation
    }
}

function Write-DevResponse($Response) {
    $json = ConvertTo-Json -InputObject $Response -Depth 48 -Compress
    if ([Text.Encoding]::UTF8.GetByteCount($json) -gt (8 * 1024 * 1024 - 1)) {
        Stop-Dev 'OUTPUT_LIMIT_EXCEEDED' 'Developer response exceeded 8 MiB; inspect state before retrying a mutation.'
    }
    [Console]::Out.WriteLine($json)
}

try {
    if ($RequestBase64.Length -gt 24000) { Stop-Dev 'INVALID_REQUEST' 'The encoded request is too large.' }
    try {
        $bytes = [Convert]::FromBase64String($RequestBase64)
        if ($bytes.Length -gt 16000) { throw 'Request too large.' }
        $utf8 = New-Object Text.UTF8Encoding($false, $true)
        $text = $utf8.GetString($bytes)
        if (!$text.TrimStart().StartsWith('{')) { throw 'Expected an object.' }
        $request = ConvertFrom-Json -InputObject $text -ErrorAction Stop
    } catch { Stop-Dev 'INVALID_REQUEST' 'RequestBase64 must contain valid UTF-8 JSON of at most 16000 bytes.' }
    Assert-DevFields $request @('action', 'params')
    $script:DevActionName = Get-DevString $request 'action' $null 128 -Required
    $parameters = Get-DevField $request 'params' ([PSCustomObject]@{})
    if (!(Test-DevObject $parameters)) { Stop-Dev 'INVALID_REQUEST' 'params must be a JSON object.' }
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { Stop-Dev 'UNSUPPORTED_PLATFORM' 'The developer helper requires Windows.' }
    $modules = Join-Path $script:DeveloperRoot 'developer'
    if (!(Test-Path -LiteralPath $modules -PathType Container)) { Stop-Dev 'INVALID_MODULE' 'Install the complete developer helper directory.' }
    foreach ($module in @(Get-ChildItem -LiteralPath $modules -Filter '*.ps1' -File | Sort-Object Name)) { . $module.FullName }
    if (!$script:DevActions.ContainsKey($script:DevActionName)) { Stop-Dev 'UNKNOWN_ACTION' 'Unknown developer action.' }
    $action = $script:DevActions[$script:DevActionName]
    $script:DevMutation = $action.mutation
    if ($action.mutation -and !$AllowWrite) { Stop-Dev 'WRITE_NOT_ALLOWED' 'This developer action requires explicit AllowWrite authorization.' }
    if ($action.interactive -and (([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) -or ![Environment]::UserInteractive)) {
        Stop-Dev 'INTERACTIVE_SESSION_REQUIRED' 'This action requires the logged-in user interactive session.'
    }
    $data = & $action.handler $parameters
    if (($null -eq $data) -or (!(Test-DevObject $data) -and ($data -isnot [Collections.IDictionary]))) {
        Stop-Dev 'INVALID_MODULE' 'Developer handlers must return one data object.'
    }
    Write-DevResponse ([ordered]@{ ok=$true; data=$data; meta=(New-DevMetadata) })
    exit 0
} catch {
    $diagnostic = [ordered]@{ exception_type=$_.Exception.GetType().Name; source_line=$_.InvocationInfo.ScriptLineNumber }
    Write-DevResponse ([ordered]@{
        ok=$false
        error=[ordered]@{ code=$script:DevFailureCode; message=$script:DevFailureMessage; diagnostic=$diagnostic }
        meta=(New-DevMetadata)
    })
    exit 1
}
