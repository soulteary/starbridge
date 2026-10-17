#requires -Version 5.1
[CmdletBinding()]
param([string]$HelperPath = (Join-Path $PSScriptRoot '..\..\guest\windows\developer-bridge.ps1'))

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$helper = (Resolve-Path -LiteralPath $HelperPath).ProviderPath
$sourceRoot = Split-Path -Parent $helper
$files = @((Get-Item -LiteralPath $helper)) + @(Get-ChildItem -LiteralPath (Join-Path $sourceRoot 'developer') -Recurse -File -Filter '*.ps1')
foreach ($file in $files) {
    $tokens = $null; $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($file.FullName, [ref]$tokens, [ref]$parseErrors)
    if ($parseErrors.Count -ne 0) { throw ($file.Name + ': ' + ($parseErrors.Message -join '; ')) }
}
$powerShellPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$temporary = Join-Path ([IO.Path]::GetTempPath()) ('starbridge-developer-test-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -Path (Join-Path $temporary 'developer') -ItemType Directory -Force
$script:Checks = 0

function Invoke-DevContract([string]$Json, [string]$Code = '', [bool]$Write = $false, [string]$Base64 = '') {
    if ($Base64 -eq '') { $Base64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($Json)) }
    $arguments = @('-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $temporary 'developer-bridge.ps1'), '-RequestBase64', $Base64)
    if ($Write) { $arguments += '-AllowWrite' }
    $stderrPath = Join-Path $temporary 'stderr.txt'
    $output = @(& $powerShellPath @arguments 2> $stderrPath)
    $exitCode = $LASTEXITCODE
    if ((Get-Item -LiteralPath $stderrPath).Length -ne 0) { throw 'Developer helper wrote unexpected stderr.' }
    if ($output.Count -ne 1) { throw ('Expected exactly one JSON line; received ' + $output.Count) }
    $response = ConvertFrom-Json -InputObject ([string]$output[0])
    if ($response.ok -isnot [bool]) { throw 'Missing boolean ok.' }
    if (($response.meta.protocol_version -cne 'dev-1') -or ($response.meta.session_id -isnot [int]) -or ($response.meta.user_interactive -isnot [bool]) -or ($response.meta.read_only -isnot [bool])) {
        throw 'Invalid developer response metadata.'
    }
    if ([string]::IsNullOrEmpty($response.meta.timestamp) -or ($response.meta.duration_ms -lt 0)) { throw 'Invalid observation metadata.' }
    if ($Code -eq '') {
        if (($exitCode -ne 0) -or !$response.ok -or ($null -eq $response.PSObject.Properties['data']) -or ($null -ne $response.PSObject.Properties['error'])) {
            throw ('Expected developer success; code={0}, type={1}, source_line={2}.' -f $response.error.code, $response.error.diagnostic.exception_type, $response.error.diagnostic.source_line)
        }
    } else {
        if (($exitCode -ne 1) -or $response.ok -or ($response.error.code -cne $Code) -or [string]::IsNullOrEmpty($response.error.message) -or ($null -ne $response.PSObject.Properties['data'])) {
            throw ('Expected {0}; received error code {1}.' -f $Code, $response.error.code)
        }
    }
    $script:Checks++
    return $response
}

try {
    Copy-Item -LiteralPath $helper -Destination (Join-Path $temporary 'developer-bridge.ps1')
    Copy-Item -LiteralPath (Join-Path $sourceRoot 'developer\system.ps1') -Destination (Join-Path $temporary 'developer\system.ps1')
    # Synthetic handlers exercise protocol behavior without launching applications,
    # reading private files, or requiring UI Automation and an interactive session.
    $fixture = @'
Register-DeveloperAction -Name 'contract.read' -Handler {
    param($p)
    Assert-DevFields $p @('text', 'number', 'flag', 'items')
    $items = Get-DevField $p 'items' @()
    if ($items -isnot [Array]) { Stop-Dev 'INVALID_REQUEST' 'items must be an array.' }
    return [ordered]@{
        text=(Get-DevString $p 'text' 'default' 2)
        number=(Get-DevInteger $p 'number' 3 0 10)
        flag=(Get-DevBoolean $p 'flag' $false)
        items=@($items)
    }
} -Mutation:$false -Interactive:$false
Register-DeveloperAction -Name 'contract.write' -Handler { param($p); Assert-DevFields $p @(); return @{ changed=$true } } -Mutation:$true
'@
    [IO.File]::WriteAllText((Join-Path $temporary 'developer\contract.ps1'), $fixture, (New-Object Text.UTF8Encoding($false)))
    $info = Invoke-DevContract '{"action":"system.info","params":{}}'
    if (($info.data.os.name -cne 'Windows') -or ($info.data.helper.sha256 -cnotmatch '^[0-9a-f]{64}$') -or ($info.data.helper.protocol_version -cne 'dev-1')) { throw 'Invalid environment identity.' }
    if (($info.data.capabilities -isnot [Array]) -or ($info.data.capabilities.Count -ne 3)) { throw 'Capabilities must be an explicit array of installed actions.' }
    if (($info.data.session.id -ne $info.meta.session_id) -or ($info.data.session.user_interactive -ne $info.meta.user_interactive)) { throw 'Environment session differs from metadata.' }
    $json = ConvertTo-Json -InputObject $info -Depth 20 -Compress
    if ($json.Contains($temporary) -or ($null -ne $info.meta.PSObject.Properties['user'])) { throw 'Environment response disclosed a private path or username field.' }
    $again = Invoke-DevContract '{"action":"system.info"}'
    if ($again.data.helper.sha256 -cne $info.data.helper.sha256) { throw 'Unchanged installed code must have a stable identity.' }
    Add-Content -LiteralPath (Join-Path $temporary 'developer\contract.ps1') -Value '# Changed fixture code.' -Encoding UTF8
    $changed = Invoke-DevContract '{"action":"system.info"}'
    if ($changed.data.helper.sha256 -ceq $info.data.helper.sha256) { throw 'Code identity did not cover installed action modules.' }
    $read = Invoke-DevContract '{"action":"contract.read","params":{}}'
    if (($read.data.text -cne 'default') -or ($read.data.number -ne 3) -or ($read.data.flag -ne $false) -or ($read.data.items -isnot [Array]) -or ($read.data.items.Count -ne 0)) { throw 'Defaults or empty arrays were not preserved.' }
    $read = Invoke-DevContract '{"action":"contract.read","params":{"items":[7],"number":1e0,"flag":true}}'
    if (($read.data.items -isnot [Array]) -or ($read.data.items.Count -ne 1) -or ($read.data.number -ne 1) -or !$read.data.flag) { throw 'Singleton array or numeric canonical value was lost.' }
    $emoji = [char]::ConvertFromUtf32(0x1F600)
    $read = Invoke-DevContract ('{"action":"contract.read","params":{"text":"' + $emoji + $emoji + '"}}')
    if ($read.data.text -cne ($emoji + $emoji)) { throw 'Unicode code points were not preserved.' }
    $null = Invoke-DevContract ('{"action":"contract.read","params":{"text":"' + $emoji + $emoji + $emoji + '"}}') 'INVALID_REQUEST'
    foreach ($invalid in @('null', '[]', '[{}]', 'true', '1', '"text"')) {
        $null = Invoke-DevContract $invalid 'INVALID_REQUEST'
        $null = Invoke-DevContract ('{"action":"contract.read","params":' + $invalid + '}') 'INVALID_REQUEST'
    }
    foreach ($params in @('{"text":null}', '{"text":[]}', '{"number":null}', '{"number":true}', '{"number":1.5}', '{"number":11}', '{"flag":1}', '{"flag":null}', '{"items":null}', '{"items":1}', '{"extra":true}', '{"Text":"x"}')) {
        $null = Invoke-DevContract ('{"action":"contract.read","params":' + $params + '}') 'INVALID_REQUEST'
    }
    $null = Invoke-DevContract '{"action":"contract.read","extra":true}' 'INVALID_REQUEST'
    $null = Invoke-DevContract '{"action":"Contract.Read"}' 'UNKNOWN_ACTION'
    $null = Invoke-DevContract '{"action":"missing.action"}' 'UNKNOWN_ACTION'
    $null = Invoke-DevContract '{"action":"system.info","params":{"path":true}}' 'INVALID_REQUEST'
    $denied = Invoke-DevContract '{"action":"contract.write"}' 'WRITE_NOT_ALLOWED'
    if ($denied.meta.read_only -ne $false) { throw 'Mutation metadata was incorrect for authorization denial.' }
    $write = Invoke-DevContract '{"action":"contract.write"}' '' $true
    if (!$write.data.changed -or $write.meta.read_only) { throw 'Authorized mutation did not run or was mislabeled.' }
    $null = Invoke-DevContract '' 'INVALID_REQUEST' $false '!!invalid-base64!!'
    $null = Invoke-DevContract '' 'INVALID_REQUEST' $false ([Convert]::ToBase64String([byte[]]@(0xC3, 0x28)))
    $null = Invoke-DevContract ('{"action":"contract.read","params":{"text":"' + ('x' * 16000) + '"}}') 'INVALID_REQUEST'
    Write-Host ('Developer protocol passed ({0} synthetic checks; Windows PowerShell {1}).' -f $script:Checks, $PSVersionTable.PSVersion)
} finally {
    Remove-Item -LiteralPath $temporary -Recurse -Force
}
