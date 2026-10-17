#requires -Version 5.1
[CmdletBinding()]
param(
    [string]$HelperPath = (Join-Path $PSScriptRoot '..\..\guest\windows\desktop-bridge.ps1'),
    [string]$FixturePath = (Join-Path $PSScriptRoot '..\fixtures\parameters.json')
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

# Load only pure parameter functions, without running the helper entry point or
# requiring an interactive desktop. The shipped helper remains a single file.
$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile((Resolve-Path $HelperPath).ProviderPath, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw 'The guest helper has syntax errors.' }
$names = @('Stop-Bridge', 'Get-Field', 'Get-Integer', 'Get-CodePointLength', 'Limit-Text', 'Get-UiOptions')
foreach ($definition in $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $true)) {
    if ($definition.Name -in $names) { . ([scriptblock]::Create($definition.Extent.Text)) }
}
$fixtures = ConvertFrom-Json -InputObject (Get-Content -LiteralPath $FixturePath -Encoding UTF8 -Raw)
$checks = 0

foreach ($case in $fixtures.transport_integer_cases) {
    $script:FailureCode = ''
    $accepted = $false
    try {
        $parameters = ConvertFrom-Json -InputObject ('{"limit":' + $case.input + '}')
        $value = Get-Integer $parameters 'limit' 500 1 1000
        $accepted = $true
    } catch {
        if ($script:FailureCode -cne 'INVALID_REQUEST') { throw }
    }
    if ($accepted -ne $case.valid) { throw ('Wrong transport acceptance: ' + $case.name) }
    if ($accepted -and ($value -ne $case.canonical)) { throw ('Wrong canonical integer: ' + $case.name) }
    $checks++
}

foreach ($case in $fixtures.defaults) {
    if ($case.action -ceq 'ui.tree') {
        $actual = Get-UiOptions $case.input
        foreach ($field in @('max_depth', 'max_nodes', 'timeout_ms')) {
            if ($actual[$field] -ne $case.canonical.$field) { throw ('Wrong UI default: ' + $field) }
        }
    } else {
        if ((Get-Integer $case.input 'limit' 500 1 1000) -ne $case.canonical.limit) { throw 'Wrong list default.' }
    }
    $checks++
}

$strictUtf8 = New-Object Text.UTF8Encoding($false, $true)
foreach ($case in $fixtures.text_cases) {
    $text = $case.unit * [int]$case.repeat
    $count = Get-CodePointLength $text
    if (($count -le 256) -ne $case.valid) { throw ('Wrong code-point limit: ' + $case.name) }
    $limited = Limit-Text ($text + 'tail') 256
    if ((Get-CodePointLength $limited) -ne [Math]::Min($count + 4, 256)) { throw ('Wrong text truncation: ' + $case.name) }
    $null = $strictUtf8.GetBytes($limited) # Fails if truncation splits a surrogate pair.
    $checks++
}
Write-Host ('Parameter contract passed ({0} cases; canonical transport integers and Unicode code points).' -f $checks)
