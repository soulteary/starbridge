#requires -Version 5.1
<# Synthetic controls and a disposable mutex-holder process only. No real
window, keyboard, mouse, application, or virtual machine is operated. #>
[CmdletBinding()]
param([string]$Root=(Join-Path $PSScriptRoot '..\..'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop'
$rootPath=(Resolve-Path -LiteralPath $Root).ProviderPath
$workers=Join-Path $rootPath 'guest\windows\developer\workers'
foreach($file in @('ui-core.ps1','ui-input-native.ps1','ui-action-core.ps1','ui-action-worker.ps1')){
    $tokens=$null;$errors=$null
    $null=[Management.Automation.Language.Parser]::ParseFile((Join-Path $workers $file),[ref]$tokens,[ref]$errors)
    if($errors.Count -ne 0){throw ('UI action syntax error: '+$file)}
}
. (Join-Path $workers 'ui-core.ps1')
. (Join-Path $workers 'ui-input-native.ps1')
. (Join-Path $workers 'ui-action-core.ps1')
$checks=0
function New-ActionParameters {
    return [PSCustomObject]@{hwnd='0x123';pid=42;start_time_utc='2026-01-02T03:04:05.1234567Z'}
}
foreach($case in @(
    @{name='window.resize';key='width';value=0},@{name='input.key';key='key';value='Win'},
    @{name='input.key';key='modifiers';value=@('Control','Control')},
    @{name='input.mouse';key='x';value=$null},@{name='window.focus';key='lock_timeout_ms';value=10000},
    @{name='ui.act';key='operation';value='execute'},@{name='ui.act';key='node_id';value='n000001'}
)){
    $p=New-ActionParameters
    if($case.name -ceq 'window.resize'){$p|Add-Member NoteProperty width 600;$p|Add-Member NoteProperty height 400}
    if($case.name -ceq 'input.key'){$p|Add-Member NoteProperty key 'A'}
    if($case.name -ceq 'input.mouse'){$p|Add-Member NoteProperty x 20;$p|Add-Member NoteProperty y 30}
    if($case.name -ceq 'ui.act'){$p|Add-Member NoteProperty operation 'invoke';$p|Add-Member NoteProperty selector ([PSCustomObject]@{name='Fixture button'})}
    $p|Add-Member NoteProperty $case.key $case.value -Force
    $script:DevUiErrorCode='';$script:DevUiMutationBegan=$false
    try{$null=Resolve-DevUiActionParameters $case.name $p;throw 'Invalid UI action was accepted.'}
    catch{if($script:DevUiErrorCode -cne 'INVALID_ARGUMENT'){throw}}
    if($script:DevUiMutationBegan){throw 'Validation dispatched an operation.'}
    $checks++
}
if((ConvertTo-DevUiVirtualKey 'F12') -ne 123 -or (ConvertTo-DevUiVirtualKey 'A') -ne 65){throw 'Wrong named-key mapping.'}
$checks++
Initialize-DevUiNative;Initialize-DevUiInputNative
$expectedSize=28;if([IntPtr]::Size -eq 8){$expectedSize=40}
if([StarbridgeDeveloperInput.Native]::InputSize() -ne $expectedSize){throw 'SendInput structure size is incorrect.'}
if([StarbridgeDeveloperInput.Native]::NormalizeAbsolute(-1920,-1920,1920) -ne 0 -or [StarbridgeDeveloperInput.Native]::NormalizeAbsolute(-1,-1920,1920) -ne 65535){throw 'Negative-monitor coordinate normalization failed.'}
$checks+=2

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -AssemblyName WindowsBase
$script:FixtureOperationCount=0;$script:FixtureThrow=$false
$patternState=[PSCustomObject]@{IsReadOnly=$false}
$patternState|Add-Member ScriptProperty Value {throw 'The synthetic password must never be read.'}
$script:FixturePattern=[PSCustomObject]@{Current=$patternState}
$script:FixturePattern|Add-Member ScriptMethod Invoke {$script:FixtureOperationCount++}
$script:FixturePattern|Add-Member ScriptMethod SetValue {param($value) if($script:FixtureThrow){throw 'synthetic-input-must-not-be-echoed'};$script:FixtureOperationCount++}
$element=[PSCustomObject]@{Current=([PSCustomObject]@{IsEnabled=$true;IsOffscreen=$false;IsPassword=$true});Supports=$true}
$element|Add-Member ScriptMethod TryGetCurrentPattern {param($requested,$output) if(!$this.Supports){return $false};$output.Value=$script:FixturePattern;return $true}
$p=New-ActionParameters;$p|Add-Member NoteProperty operation 'invoke';$p|Add-Member NoteProperty selector ([PSCustomObject]@{name='Fixture button'})
$parameters=Resolve-DevUiActionParameters 'ui.act' $p
$script:DevUiMutationBegan=$false
$output=@(Invoke-DevUiSemanticOperation $element $parameters)
if($script:FixtureOperationCount -ne 1 -or !$script:DevUiMutationBegan -or $output.Count -ne 0){throw 'Invoke was not dispatched exactly once without an input echo.'}
$parameters.operation='set_value';$parameters['value']='synthetic-input-must-not-be-echoed'
$script:DevUiMutationBegan=$false
$output=@(Invoke-DevUiSemanticOperation $element $parameters)
if($script:FixtureOperationCount -ne 2 -or !$script:DevUiMutationBegan -or $output.Count -ne 0){throw 'Password write read or echoed a value.'}
$checks+=2
$script:FixtureThrow=$true;$script:DevUiErrorCode='';$script:DevUiMutationBegan=$false
try{Invoke-DevUiSemanticOperation $element $parameters;throw 'Expected provider failure.'}
catch{if($script:DevUiErrorCode -cne 'UI_OPERATION_FAILED' -or $script:DevUiErrorMessage.Contains('synthetic-input-must-not-be-echoed') -or !$script:DevUiMutationBegan){throw 'Provider failure leaked input or lost unknown-outcome state.'}}
$script:FixtureThrow=$false
$checks++
$parameters.operation='invoke';$element.Current.IsEnabled=$false;$script:DevUiMutationBegan=$false;$script:DevUiErrorCode=''
try{Invoke-DevUiSemanticOperation $element $parameters;throw 'Disabled control was invoked.'}
catch{if($script:DevUiErrorCode -cne 'ELEMENT_DISABLED'){throw}}
if($script:DevUiMutationBegan -or $script:FixtureOperationCount -ne 2){throw 'Disabled-control rejection dispatched an operation.'}
$element.Current.IsEnabled=$true;$element.Supports=$false;$script:DevUiErrorCode=''
try{Invoke-DevUiSemanticOperation $element $parameters;throw 'Unsupported pattern was invoked.'}
catch{if($script:DevUiErrorCode -cne 'UNSUPPORTED_PATTERN'){throw}}
if($script:DevUiMutationBegan -or $script:FixtureOperationCount -ne 2){throw 'Unsupported-pattern rejection dispatched an operation.'}
$checks+=2

# A second process holds exactly the production named mutex. The parent cannot
# acquire it, even though these independent processes run in the same session.
$temporary=Join-Path ([IO.Path]::GetTempPath()) ('starbridge-action-contract-'+[Guid]::NewGuid().ToString('N'))
$child=$null;$started=$false
try{
    $null=New-Item -ItemType Directory -Path $temporary
    $holder=Join-Path $temporary 'hold-mutex.ps1';$ready=Join-Path $temporary 'ready'
    $source=@'
param([string]$MutexName,[string]$ReadyPath)
$mutex=New-Object Threading.Mutex($false,$MutexName)
try{
    if(!$mutex.WaitOne(0)){throw 'Fixture lock was unavailable.'}
    [IO.File]::WriteAllText($ReadyPath,'ready')
    Start-Sleep -Seconds 20
}finally{try{$mutex.ReleaseMutex()}finally{$mutex.Dispose()}}
'@
    [IO.File]::WriteAllText($holder,$source,(New-Object Text.UTF8Encoding($false)))
    $command="& '"+$holder.Replace("'","''")+"' -MutexName '"+(Get-DevUiSessionMutexName)+"' -ReadyPath '"+$ready.Replace("'","''")+"'"
    $start=New-Object Diagnostics.ProcessStartInfo
    $start.FileName=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $start.Arguments='-NoLogo -NoProfile -NonInteractive -Mta -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true
    $child=New-Object Diagnostics.Process;$child.StartInfo=$start;$started=$child.Start()
    if(!$started){throw 'The disposable mutex holder did not start.'}
    $timer=[Diagnostics.Stopwatch]::StartNew()
    while(!(Test-Path -LiteralPath $ready)){
        if($child.HasExited -or $timer.Elapsed.TotalSeconds -gt 10){throw 'The disposable mutex holder did not become ready.'}
        Start-Sleep -Milliseconds 50
    }
    $script:DevUiErrorCode='';$script:DevUiMutationBegan=$false
    try{$lease=Enter-DevUiSessionLease 0;$lease.ReleaseMutex();$lease.Dispose();throw 'Two processes acquired the same session lock.'}
    catch{if($script:DevUiErrorCode -cne 'SESSION_BUSY'){throw}}
    if($script:DevUiMutationBegan){throw 'Busy-session rejection dispatched an operation.'}
    $checks++
}finally{
    if($null -ne $child){try{if($started -and !$child.HasExited){$child.Kill();$null=$child.WaitForExit(3000)}}finally{$child.Dispose()}}
    if(Test-Path -LiteralPath $temporary){Remove-Item -LiteralPath $temporary -Recurse -Force}
}
Write-Host ('Developer UI action contract passed ({0} synthetic checks; no real UI input).' -f $checks)
