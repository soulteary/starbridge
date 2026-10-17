#requires -Version 5.1
<# Pure parameters, synthetic UIA objects, and a disposable sleeping worker.
No visible desktop, VM, real application, or local user information is queried. #>
[CmdletBinding()]
param([string]$Root = (Join-Path $PSScriptRoot '..\..'))
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$rootPath = (Resolve-Path -LiteralPath $Root).ProviderPath
$modulePath = Join-Path $rootPath 'guest\windows\developer\ui-query.ps1'
$corePath = Join-Path $rootPath 'guest\windows\developer\workers\ui-core.ps1'
foreach ($file in @($modulePath, $corePath, (Join-Path $rootPath 'guest\windows\developer\workers\ui-query-worker.ps1'))) {
    $tokens=$null; $errors=$null
    $null = [Management.Automation.Language.Parser]::ParseFile($file,[ref]$tokens,[ref]$errors)
    if ($errors.Count -ne 0) { throw ('UI query script has syntax errors: ' + [IO.Path]::GetFileName($file)) }
}
. $corePath
$checks = 0
function New-QueryParameters {
    return [PSCustomObject]@{hwnd='0X000123';pid=42;start_time_utc='2026-01-02T03:04:05.1234567Z'}
}
foreach ($change in @(
    @{key='pid';value=0}, @{key='hwnd';value='0'}, @{key='hwnd';value='0x8000000000000000'},
    @{key='start_time_utc';value='2026-01-02'}, @{key='selector';value=$null},
    @{key='selector';value=([PSCustomObject]@{unknown='bad'})}, @{key='max_scan_nodes';value=5001},
    @{key='include_value';value='true'}, @{key='timeout_ms';value=20001}
)) {
    $p=New-QueryParameters
    $p | Add-Member NoteProperty $change.key $change.value -Force
    $script:DevUiErrorCode=''
    try { $null=Resolve-DevUiQueryParameters $p; throw 'An invalid UI parameter was accepted.' }
    catch { if ($script:DevUiErrorCode -cne 'INVALID_ARGUMENT') { throw } }
    $checks++
}
$p = Resolve-DevUiQueryParameters (New-QueryParameters)
if ($p.hwnd -cne '0x123' -or $p.limit -ne 50 -or $p.max_scan_nodes -ne 2000 -or $p.include_value) { throw 'Wrong UI query defaults.' }
$checks++
$one=New-QueryParameters; $one | Add-Member NoteProperty mode 'one'
if ((Resolve-DevUiQueryParameters $one).limit -ne 1) { throw 'Unique mode must default to one result.' }
$checks++

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -AssemblyName WindowsBase
Initialize-DevUiNative # Compile native definitions; no window API is invoked.
$info = [PSCustomObject]@{
    Name='Fixture Input';AutomationId='FixtureInput';ClassName='Edit';FrameworkId='Win32';ProcessId=42
    ControlType=([PSCustomObject]@{ProgrammaticName='ControlType.Edit'})
    IsEnabled=$true;IsOffscreen=$false;IsPassword=$false
    BoundingRectangle=([PSCustomObject]@{IsEmpty=$false;X=-20;Y=40;Width=100;Height=30})
}
if (!(Test-DevUiSelector $info ([ordered]@{name='Fixture Input';automation_id='FixtureInput';control_type='Edit'}))) { throw 'Exact selector failed.' }
if (!(Test-DevUiSelector $info ([ordered]@{name_contains='fixture'}))) { throw 'Case-insensitive substring selector failed.' }
if (Test-DevUiSelector $info ([ordered]@{name='fixture input'})) { throw 'Exact names must be case sensitive.' }
$checks+=3

$script:DevUiOptions=$p
$script:DevUiWarnings=New-Object 'Collections.Generic.List[string]'
$script:DevUiComplete=$true
$script:UiQueryFixtureValueReads=0
$currentValue=[PSCustomObject]@{IsReadOnly=$false}
$currentValue | Add-Member ScriptProperty Value { $script:UiQueryFixtureValueReads++; return 'fixture-value' }
$script:UiQueryFixturePattern=[PSCustomObject]@{Current=$currentValue}
$element=[PSCustomObject]@{Current=$info}
$element | Add-Member ScriptMethod GetRuntimeId { return ,@(1,2) }
$element | Add-Member ScriptMethod GetSupportedPatterns { return ,@([PSCustomObject]@{ProgrammaticName='ValuePatternIdentifiers.Pattern'}) }
$element | Add-Member ScriptMethod TryGetCurrentPattern {
    param($pattern,$output)
    if ($pattern.Id -eq [System.Windows.Automation.ValuePattern]::Pattern.Id) {
        $output.Value=$script:UiQueryFixturePattern
        return $true
    }
    return $false
}
$item=Get-DevUiItem $element $info 'n000001' $null 0
if ($script:UiQueryFixtureValueReads -ne 0 -or $null -ne $item.value -or $item.value_status -cne 'not_requested') { throw 'A value was read without opt-in.' }
$script:DevUiOptions.include_value=$true
$item=Get-DevUiItem $element $info 'n000001' $null 0
if ($item.value -cne 'fixture-value' -or $item.value_status -cne 'available' -or $script:UiQueryFixtureValueReads -ne 1) { throw 'Opt-in value query failed.' }
$info.IsPassword=$true
$item=Get-DevUiItem $element $info 'n000001' $null 0
if ($null -ne $item.value -or $item.value_status -cne 'password_skipped' -or $script:UiQueryFixtureValueReads -ne 1 -or $null -ne $item.state.value_read_only) { throw 'Password contents or state were read.' }
$checks+=3

# A text-only provider counts GetText length in UTF-16 units. Its synthetic
# strings exercise the public code-point cap without touching an actual UI.
$info.IsPassword=$false
$range=[PSCustomObject]@{Text='';LastMaxLength=0}
$range | Add-Member ScriptMethod GetText {
    param([int]$MaxLength)
    $this.LastMaxLength=$MaxLength
    return $this.Text.Substring(0,[Math]::Min($this.Text.Length,$MaxLength))
}
$script:UiQueryFixtureTextPattern=[PSCustomObject]@{DocumentRange=$range}
$textElement=[PSCustomObject]@{Current=$info}
$textElement | Add-Member ScriptMethod GetRuntimeId { return ,@(1,3) }
$textElement | Add-Member ScriptMethod GetSupportedPatterns { return ,@([PSCustomObject]@{ProgrammaticName='TextPatternIdentifiers.Pattern'}) }
$textElement | Add-Member ScriptMethod TryGetCurrentPattern {
    param($pattern,$output)
    if ($pattern.Id -eq [System.Windows.Automation.TextPattern]::Pattern.Id) { $output.Value=$script:UiQueryFixtureTextPattern;return $true }
    return $false
}
$emoji=[char]::ConvertFromUtf32(0x1F600)
$replacement=[string][char]0xFFFD
$strictUtf8=New-Object Text.UTF8Encoding($false,$true)
foreach ($case in @(
    @{text=($emoji*2048);expected=($emoji*2048);truncated=$false},
    @{text=($emoji*2049);expected=($emoji*2048);truncated=$true},
    @{text=(('x'*2047)+$emoji+'z');expected=(('x'*2047)+$emoji);truncated=$true},
    @{text=('x'*2048);expected=('x'*2048);truncated=$false},
    @{text=('fixture'+[char]0xD83D);expected=('fixture'+$replacement);truncated=$false},
    @{text=([string][char]0xDE00+'fixture');expected=($replacement+'fixture');truncated=$false}
)) {
    $range.Text=$case.text;$range.LastMaxLength=0
    $item=Get-DevUiItem $textElement $info 'n000001' $null 0
    if ($range.LastMaxLength -ne 4098 -or $item.value_status -cne 'available' -or $item.value -cne $case.expected -or $item.value_truncated -ne $case.truncated -or (Get-DevUiCodePointLength $item.value) -gt 2048) {
        throw 'TextPattern code-point budget or truncation flag is incorrect.'
    }
    $null=$strictUtf8.GetByteCount($item.value) # Reject any unpaired surrogate.
    $checks++
}
$range.LastMaxLength=0;$info.IsPassword=$true
$item=Get-DevUiItem $textElement $info 'n000001' $null 0
if ($range.LastMaxLength -ne 0 -or $item.value_status -cne 'password_skipped' -or $null -ne $item.value) { throw 'Password TextPattern was read.' }
$info.IsPassword=$false
$checks++

# Exercise the production traversal against an in-memory tree. Pattern reading
# above is tested separately, so this replacement only serializes match identity.
function Get-DevUiItem($Element,$Info,[string]$NodeId,$ParentId,[int]$Depth) {
    return [ordered]@{node_id=$NodeId;name=$Info.Name;depth=$Depth}
}
function New-FakeQueryNode([string]$Name,[object[]]$Children=@()) {
    $node=[PSCustomObject]@{
        Cached=([PSCustomObject]@{Name=$Name;AutomationId=$Name;ClassName='Fixture';ControlType=([PSCustomObject]@{ProgrammaticName='ControlType.Button'})})
        Children=$Children;Next=$null
    }
    for ($i=0;$i -lt ($Children.Count-1);$i++) { $Children[$i].Next=$Children[$i+1] }
    return $node
}
$tree=New-FakeQueryNode 'root' @((New-FakeQueryNode 'panel' @((New-FakeQueryNode 'match'),(New-FakeQueryNode 'match'))),(New-FakeQueryNode 'other'))
$script:DevUiWalker=New-Object PSObject
$script:DevUiWalker | Add-Member ScriptMethod GetFirstChild { param($node,$cache=$null) if ($node.Children.Count -gt 0) { return $node.Children[0] }; return $null }
$script:DevUiWalker | Add-Member ScriptMethod GetNextSibling { param($node,$cache=$null) return $node.Next }
$script:DevUiCache=$null
function Reset-FakeQuery([int]$Budget,[int]$Offset) {
    $script:DevUiScanned=0;$script:DevUiMatched=0;$script:DevUiComplete=$true;$script:DevUiNodeLimit=$false;$script:DevUiDepthLimit=$false
    $script:DevUiItems=New-Object 'Collections.Generic.List[object]'
    $script:DevUiWarnings=New-Object 'Collections.Generic.List[string]'
    $script:DevUiOptions=Resolve-DevUiQueryParameters (New-QueryParameters)
    $script:DevUiOptions.selector=[ordered]@{name='match'}
    $script:DevUiOptions.ancestor=[ordered]@{name='panel'}
    $script:DevUiOptions.limit=1;$script:DevUiOptions.offset=$Offset;$script:DevUiOptions.max_scan_nodes=$Budget
}
Reset-FakeQuery 20 1
Visit-DevUiQueryNode $tree $null 0 @()
if ($script:DevUiMatched -ne 2 -or $script:DevUiItems.Count -ne 1 -or $script:DevUiItems[0].node_id -cne 'n000004' -or !$script:DevUiComplete) { throw 'Ancestor filtering or live pagination failed.' }
Reset-FakeQuery 2 0
Visit-DevUiQueryNode $tree $null 0 @()
if ($script:DevUiScanned -ne 2 -or !$script:DevUiNodeLimit -or $script:DevUiComplete) { throw 'The traversal exceeded or failed to report its scan budget.' }
$checks+=2

# Load only the worker-launch function; do not register or execute an entry.
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($modulePath,[ref]$tokens,[ref]$errors)
foreach ($definition in $ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst]},$true)) {
    if ($definition.Name -ceq 'Invoke-DevUiWorker') { . ([scriptblock]::Create($definition.Extent.Text)) }
}
function Stop-Dev([string]$Code,[string]$Message,$Details=$null) { $script:UiQueryWorkerError=$Code;$script:UiQueryWorkerDetails=$Details;throw $Message }
$temporary=Join-Path ([IO.Path]::GetTempPath()) ('starbridge-query-contract-' + [Guid]::NewGuid().ToString('N'))
try {
    $workers=Join-Path $temporary 'developer\workers'
    $null=New-Item -ItemType Directory -Path $workers -Force
    [IO.File]::WriteAllText((Join-Path $workers 'fixture-sleep.ps1'),'param([string]$RequestBase64,[switch]$AllowWrite) Start-Sleep -Seconds 20',(New-Object Text.UTF8Encoding($false)))
    $script:DeveloperRoot=$temporary;$script:UiQueryWorkerError=''
    $timer=[Diagnostics.Stopwatch]::StartNew()
    try { $null=Invoke-DevUiWorker 'fixture-sleep.ps1' ([PSCustomObject]@{}) 1000;throw 'A sleeping worker did not time out.' }
    catch { if ($script:UiQueryWorkerError -cne 'UIA_TIMEOUT') { throw } }
    $timer.Stop()
    if ($timer.Elapsed.TotalSeconds -gt 8) { throw 'Worker timeout was not bounded.' }
    $checks++
    try { $null=Invoke-DevUiWorker 'fixture-sleep.ps1' ([PSCustomObject]@{}) 1000 $true;throw 'A synthetic mutation worker did not time out.' }
    catch { if ($script:UiQueryWorkerError -cne 'ACTION_TIMEOUT' -or $script:UiQueryWorkerDetails.outcome -cne 'unknown') { throw } }
    $checks++
    $errorWorker=@'
param([string]$RequestBase64,[switch]$AllowWrite)
$p=ConvertFrom-Json -InputObject ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RequestBase64)))
$result=@{ok=$false;error=@{code='FIXTURE_FAILURE';message='Synthetic worker error.';details=@{outcome=$p.outcome;sensitive='synthetic-input-must-not-be-forwarded'}}}
[Console]::Out.WriteLine((ConvertTo-Json -InputObject $result -Depth 8 -Compress))
exit 1
'@
    [IO.File]::WriteAllText((Join-Path $workers 'fixture-error.ps1'),$errorWorker,(New-Object Text.UTF8Encoding($false)))
    foreach ($case in @(
        @{outcome='not_started';expected='not_started'},@{outcome='unknown';expected='unknown'},
        @{outcome='synthetic-input-must-not-be-forwarded';expected='unknown'},@{outcome=$null;expected='unknown'}
    )) {
        try { $null=Invoke-DevUiWorker 'fixture-error.ps1' ([PSCustomObject]@{outcome=$case.outcome}) 5000 $true;throw 'Synthetic worker failure was accepted.' }
        catch { if ($script:UiQueryWorkerError -cne 'FIXTURE_FAILURE') { throw } }
        if ($script:UiQueryWorkerDetails.Count -ne 1 -or $script:UiQueryWorkerDetails.outcome -cne $case.expected -or (ConvertTo-Json $script:UiQueryWorkerDetails).Contains('synthetic-input-must-not-be-forwarded')) {
            throw 'Worker outcome was lost or raw worker details were forwarded.'
        }
        $checks++
    }
    try { $null=Invoke-DevUiWorker 'fixture-error.ps1' ([PSCustomObject]@{outcome='unknown'}) 5000;throw 'Synthetic read worker failure was accepted.' }
    catch { if ($script:UiQueryWorkerError -cne 'FIXTURE_FAILURE' -or $null -ne $script:UiQueryWorkerDetails) { throw } }
    $checks++
    [IO.File]::WriteAllText((Join-Path $workers 'fixture-invalid.ps1'),'param([string]$RequestBase64,[switch]$AllowWrite) [Console]::Out.WriteLine(''invalid synthetic output'')',(New-Object Text.UTF8Encoding($false)))
    try { $null=Invoke-DevUiWorker 'fixture-invalid.ps1' ([PSCustomObject]@{}) 5000 $true;throw 'Malformed worker response was accepted.' }
    catch { if ($script:UiQueryWorkerError -cne 'UIA_WORKER_FAILED' -or $script:UiQueryWorkerDetails.outcome -cne 'unknown') { throw } }
    $checks++
    try { $null=Invoke-DevUiWorker 'fixture-missing.ps1' ([PSCustomObject]@{}) 5000 $true;throw 'Missing worker was accepted.' }
    catch { if ($script:UiQueryWorkerError -cne 'UIA_WORKER_UNAVAILABLE' -or $script:UiQueryWorkerDetails.outcome -cne 'not_started') { throw } }
    $checks++

    # Run the real public entry against a synthetic noninteractive action. It
    # invokes the same worker launcher and actual Stop-Dev/JSON envelope code.
    $entry=Join-Path $temporary 'developer-bridge.ps1'
    Copy-Item -LiteralPath (Join-Path $rootPath 'guest\windows\developer-bridge.ps1') -Destination $entry
    Copy-Item -LiteralPath $modulePath -Destination (Join-Path $temporary 'developer\ui-query.ps1')
    $fixtureModule=@'
Register-DeveloperAction -Name 'contract.uiworker' -Mutation:$true -Handler {
    param($p)
    Invoke-DevUiWorker 'fixture-error.ps1' $p 5000 $true
}
'@
    [IO.File]::WriteAllText((Join-Path $temporary 'developer\fixture.ps1'),($fixtureModule+[Environment]::NewLine),(New-Object Text.UTF8Encoding($false)))
    foreach ($expected in @('not_started','unknown')) {
        $request=ConvertTo-Json -InputObject @{action='contract.uiworker';params=@{outcome=$expected}} -Depth 5 -Compress
        $request64=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
        $command="& '"+$entry.Replace("'","''")+"' -RequestBase64 '"+$request64+"' -AllowWrite"
        $start=New-Object Diagnostics.ProcessStartInfo
        $start.FileName=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
        $start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
        $start.UseShellExecute=$false;$start.CreateNoWindow=$true
        $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
        $start.StandardOutputEncoding=New-Object Text.UTF8Encoding($false)
        $start.StandardErrorEncoding=New-Object Text.UTF8Encoding($false)
        $child=New-Object Diagnostics.Process;$child.StartInfo=$start
        try {
            if(!$child.Start()){throw 'Synthetic public entry did not start.'}
            $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
            if(!$child.WaitForExit(12000)){
                try{$child.Kill();$null=$child.WaitForExit(2000)}catch{}
                throw 'Synthetic public entry exceeded its deadline.'
            }
            $output=$stdout.GetAwaiter().GetResult();$errorOutput=$stderr.GetAwaiter().GetResult()
            $response=ConvertFrom-Json -InputObject $output
            if($child.ExitCode -ne 1 -or $errorOutput.Length -ne 0 -or $response.ok -ne $false -or $response.error.code -cne 'FIXTURE_FAILURE' -or
                $response.error.details.outcome -cne $expected -or @($response.error.details.PSObject.Properties).Count -ne 1 -or
                $response.meta.action -cne 'contract.uiworker' -or $response.meta.read_only -ne $false -or $output.Contains('synthetic-input-must-not-be-forwarded')) {
                throw 'The public mutation failure envelope lost its outcome or exposed raw worker details.'
            }
        } finally { $child.Dispose() }
        $checks++
    }
} finally { if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Recurse -Force } }
Write-Host ('Developer UI query contract passed ({0} synthetic checks).' -f $checks)
