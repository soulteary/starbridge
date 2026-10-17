#requires -Version 5.1
<# Pure Unicode event construction and synthetic insertion delegates only.
No real window is inspected and SendInput is never called by this test. #>
[CmdletBinding()]
param([string]$Root=(Join-Path $PSScriptRoot '..\..'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop'
$rootPath=(Resolve-Path -LiteralPath $Root).ProviderPath
$workers=Join-Path $rootPath 'guest\windows\developer\workers'
foreach($file in @('ui-core.ps1','ui-input-native.ps1','ui-action-core.ps1','ui-action-worker.ps1')){
    $tokens=$null;$errors=$null
    $null=[Management.Automation.Language.Parser]::ParseFile((Join-Path $workers $file),[ref]$tokens,[ref]$errors)
    if($errors.Count -ne 0){throw ('Input text syntax error: '+$file)}
}
. (Join-Path $workers 'ui-core.ps1')
. (Join-Path $workers 'ui-input-native.ps1')
. (Join-Path $workers 'ui-action-core.ps1')
function Register-DeveloperAction([string]$Name,[string]$Description,[switch]$Mutation,[switch]$Interactive,[scriptblock]$Handler){
    if($Name -ceq 'input.text'){
        if(!$Mutation -or !$Interactive -or $null -eq $Handler){throw 'Text registration omitted its write/session boundary.'}
        $script:TextActionRegistered=$true
    }
}
$script:TextActionRegistered=$false
. (Join-Path $rootPath 'guest\windows\developer\ui-actions.ps1')
if(!$script:TextActionRegistered){throw 'Text action was not installed by its module.'}
function Stop-Dev([string]$Code,[string]$Message,$Details){
    $script:WireFailureCode=$Code;$script:WireFailureDetails=$Details
    throw $Message
}
Initialize-DevUiInputNative
$checks=0
$emoji=[char]::ConvertFromUtf32(0x1F600)
$chinese=([string][char]0x4E2D)+([string][char]0x6587)
function New-TextParameters($Value){
    return [PSCustomObject]@{hwnd='0x123';pid=42;start_time_utc='2026-01-02T03:04:05Z';text=$Value}
}
foreach($text in @('a',($chinese+$emoji+"`r`n`r`n`t"),([string][char]0xFFFD),($emoji*2048),('a'*2048))){
    $p=Resolve-DevUiActionParameters 'input.text' (New-TextParameters $text)
    if($p.text -cne $text){throw 'Text normalization changed literal characters.'}
    $events=[StarbridgeDeveloperInput.Native]::BuildUnicodeTextEvents($text)
    if($events.Length -ne $text.Length*2 -or $events.Length -gt 8192){throw 'Wrong UTF-16 event budget.'}
    for($i=0;$i -lt $text.Length;$i++){
        $down=$events[$i*2];$up=$events[$i*2+1]
        if($down.VirtualKey -ne 0 -or $up.VirtualKey -ne 0 -or $down.ScanCode -ne [int][char]$text[$i] -or $up.ScanCode -ne $down.ScanCode -or $down.Flags -ne 4 -or $up.Flags -ne 6){
            throw 'Unicode input changed a code unit or synthesized an ordinary virtual key.'
        }
    }
    $checks++
}
foreach($value in @($null,42,$true,'',("a"+[char]0+"b"),([string][char]0xD800),([string][char]0xDFFF),('a'*2049),($emoji*2049))){
    $script:DevUiErrorCode='';$script:DevUiMutationBegan=$false
    try{$null=Resolve-DevUiActionParameters 'input.text' (New-TextParameters $value);throw 'Invalid text was accepted.'}
    catch{if($script:DevUiErrorCode -cne 'INVALID_ARGUMENT'){throw}}
    if($script:DevUiMutationBegan){throw 'Invalid text dispatched input.'}
    $checks++
}
# Pre-serialization validation is needed even when callers bypass the Go host.
foreach($value in @($null,42,'',([string][char]0),([string][char]0xD800),([string][char]0xDFFF))){
    $script:WireFailureCode='';$script:WireFailureDetails=$null
    try{Assert-DevUiTextWire (New-TextParameters $value);throw 'Invalid Unicode entered worker serialization.'}
    catch{if($script:WireFailureCode -cne 'INVALID_ARGUMENT' -or $script:WireFailureDetails.outcome -cne 'not_started'){throw}}
    $checks++
}
Assert-DevUiTextWire (New-TextParameters ($chinese+$emoji+"`r`n`t"))
foreach($text in @('',([string][char]0xD800),([string][char]0xDFFF),([string][char]0),('a'*2049),($emoji*2049))){
    $rejected=$false
    try{$null=[StarbridgeDeveloperInput.Native]::BuildUnicodeTextEvents($text)}catch{$rejected=$true}
    if(!$rejected){throw 'Native constructor accepted invalid or oversized text.'}
    $checks++
}

# The same dispatch algorithm is used by production, but the insertion delegate
# below records event arrays in memory instead of calling user32 SendInput.
$sender=[Func[StarbridgeDeveloperInput.UnicodeInputEvent[],int]]{
    param($events)
    $script:TextInsertCalls.Add($events.Clone())
    if($script:TextInsertThrows){throw 'synthetic-private-input-canary'}
    return [int]$script:TextInserted
}
$text='a'+$emoji
foreach($sent in @(0,1,2,3,5,6,7,-1)){
    $script:TextInsertCalls=New-Object 'Collections.Generic.List[object]'
    $script:TextInsertThrows=$false;$script:TextInserted=$sent
    $count=[StarbridgeDeveloperInput.Native]::DispatchUnicodeText($text,$sender)
    if($sent -eq 6){if($count -ne 6 -or $script:TextInsertCalls.Count -ne 1){throw 'Complete insertion was repeated.'}}
    else{
        if($count -ne -1){throw 'Partial insertion was reported as successful.'}
        $expectedCalls=1
        if($sent -gt 0 -and $sent -lt 6 -and $sent%2 -eq 1){$expectedCalls=2}
        if($script:TextInsertCalls.Count -ne $expectedCalls){throw 'Partial insertion retried text or released unrelated events.'}
        if($expectedCalls -eq 2){
            $release=$script:TextInsertCalls[1]
            $initial=$script:TextInsertCalls[0]
            if($release.Length -ne 1 -or $release[0].Flags -ne 6 -or $release[0].VirtualKey -ne 0 -or $release[0].ScanCode -ne $initial[$sent].ScanCode){throw 'Cleanup was not limited to the unmatched Unicode key-up.'}
        }
    }
    $checks++
}
$script:TextInsertCalls=New-Object 'Collections.Generic.List[object]';$script:TextInsertThrows=$true
if([StarbridgeDeveloperInput.Native]::DispatchUnicodeText($text,$sender) -ne -1 -or $script:TextInsertCalls.Count -ne 1){throw 'Insertion exception leaked or retried the operation.'}
$checks++

# Exercise the PowerShell mutation boundary with a synthetic native facade.
# This replacement is local to the test process; production retains user32.
function Send-DevUiUnicodeText([string]$Text){
    $script:FacadeCalls++
    if($script:FacadeThrow){throw 'synthetic-private-input-canary'}
    return $script:FacadeResult
}
$params=Resolve-DevUiActionParameters 'input.text' (New-TextParameters ($chinese+$emoji+"`r`n`t"))
foreach($mode in @('complete','partial','exception')){
    $script:FacadeCalls=0;$script:FacadeThrow=($mode -ceq 'exception')
    $script:FacadeResult=-1;if($mode -ceq 'complete'){$script:FacadeResult=$params.text.Length*2}
    $script:DevUiMutationBegan=$false;$script:DevUiErrorCode=''
    if($mode -ceq 'complete'){
        $data=Invoke-DevUiTextDispatch $params
        if($data.input_count -ne $params.text.Length*2 -or $data.utf16_unit_count -ne $params.text.Length -or $data.code_point_count -ne 6 -or $data.newline_policy -cne 'literal' -or (ConvertTo-Json $data -Compress).Contains('synthetic-private-input-canary')){throw 'Dispatch metadata was incorrect or echoed text.'}
    }else{
        try{$null=Invoke-DevUiTextDispatch $params;throw 'Unknown mutation was reported successful.'}
        catch{if($script:DevUiErrorCode -cne 'INPUT_FAILED' -or $script:DevUiErrorMessage.Contains('synthetic-private-input-canary')){throw 'Input failure leaked a provider exception.'}}
    }
    if(!$script:DevUiMutationBegan -or $script:FacadeCalls -ne 1){throw 'Mutation outcome was lost or the operation was retried.'}
    $checks++
}
Write-Host ('Developer input text contract passed ({0} synthetic checks; no real UI input).' -f $checks)
