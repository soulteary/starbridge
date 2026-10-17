#requires -Version 5.1
<# Generated pixels, DPAPI on synthetic metadata, and in-memory native stubs.
No window is inspected, no input is sent, and no VM/application is launched. #>
[CmdletBinding()]
param([string]$Root=(Join-Path $PSScriptRoot '..\..'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop'
$workers=Join-Path (Resolve-Path -LiteralPath $Root).ProviderPath 'guest\windows\developer\workers'
$module=Join-Path (Split-Path -Parent $workers) 'visual-flow.ps1'
foreach($file in @($module,(Join-Path $workers 'visual-worker.ps1'),(Join-Path $workers 'visual-core.ps1'))){
    $tokens=$null;$errors=$null;$null=[Management.Automation.Language.Parser]::ParseFile($file,[ref]$tokens,[ref]$errors)
    if($errors.Count -gt 0){throw ('Visual syntax error: '+[IO.Path]::GetFileName($file))}
}
. (Join-Path $workers 'ui-core.ps1')
. (Join-Path $workers 'ui-input-native.ps1')
. (Join-Path $workers 'ui-action-core.ps1')
. (Join-Path $workers 'screenshot-core.ps1')
. (Join-Path $workers 'visual-core.ps1')
# Native substitutes only implement the APIs invoked by the production visual
# coordinator. Input policy/SendInput itself is covered by the UI action suite.
Add-Type -TypeDefinition @'
using System;
namespace StarbridgeDeveloperUi { public static class Native {
 public static IntPtr EnterDpi(){return new IntPtr(1);} public static void LeaveDpi(IntPtr value){}
} }
namespace StarbridgeDeveloperInput {
 public sealed class Rect { public int x,y,width,height; }
 public static class Native {
  public static Rect Geometry=new Rect{x=-1008,y=-108,width=216,height=116};
  public static Rect WindowRect(IntPtr value){return new Rect{x=Geometry.x,y=Geometry.y,width=Geometry.width,height=Geometry.height};}
 }
}
namespace StarbridgeDeveloperVisual { public static class Native {
 public static int CurrentDpi=144; public static int Dpi(IntPtr value){return CurrentDpi;}
} }
'@
$script:Checks=0;$script:Clicks=0;$script:Focuses=0;$script:Leases=0;$script:Captures=0;$script:FailPost=$false;$script:IdentityValid=$true;$script:SessionId=7
function Assert-VisualTest([bool]$Condition,[string]$Label){if(!$Condition){throw $Label};$script:Checks++}
function Expect-VisualError([scriptblock]$Body,[string]$Code){
    $script:DevUiErrorCode='';try{& $Body;throw 'Expected visual failure was accepted.'}catch{if($script:DevUiErrorCode -cne $Code){throw}}
    $script:Checks++
}
function Copy-VisualObject($Object){return (ConvertFrom-Json -InputObject (ConvertTo-Json -InputObject $Object -Depth 16 -Compress))}
function Assert-DevVisualSession {}
function Get-DevVisualSessionId {return $script:SessionId}
function Assert-DevUiWindow($Parameters){if(!$script:IdentityValid){Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'Synthetic identity changed.'};return [IntPtr]0x123}
function Enter-DevUiSessionLease([int]$TimeoutMs){
    $script:Leases++;$lease=[PSCustomObject]@{}
    $lease|Add-Member ScriptMethod ReleaseMutex {$script:Leases--}
    $lease|Add-Member ScriptMethod Dispose {}
    return $lease
}
function Invoke-DevUiActionCore([string]$Name,$Parameters,[switch]$SessionLeaseHeld){
    Assert-VisualTest ($SessionLeaseHeld -and $script:Leases -eq 1) 'Visual input reacquired or omitted its session lease.'
    $script:DevUiMutationBegan=$true
    if($Name -ceq 'window.focus'){$script:Focuses++}
    elseif($Name -ceq 'input.mouse'){$script:Clicks++;$script:LastPoint=@($Parameters.x,$Parameters.y)}
    else{throw 'Unexpected nested mutation.'}
    return [ordered]@{outcome='dispatched'}
}
Add-Type -AssemblyName System.Drawing
$bitmap=New-Object Drawing.Bitmap(200,100);$stream=New-Object IO.MemoryStream
try{$bitmap.Save($stream,[Drawing.Imaging.ImageFormat]::Png);$script:Pixels=$stream.ToArray()}finally{$bitmap.Dispose();$stream.Dispose()}
function Invoke-DevScreenshotCore($Parameters){
    $script:Captures++
    if($script:FailPost -and $script:Clicks -gt 0){Stop-DevUi 'SCREENSHOT_RENDER_FAILED' 'Synthetic post-action capture failure.'}
    $stamp=[DateTime]::UtcNow
    return ConvertTo-DevScreenshotResult $Parameters ([PSCustomObject]@{x=-1000;y=-100;width=200;height=100}) $script:Pixels $stamp $stamp
}
$identity=[PSCustomObject]@{hwnd='0x123';pid=42;start_time_utc='2026-01-02T03:04:05Z';include_uia=$false}
$observe=Resolve-DevVisualParameters 'ui.observe' $identity
$first=Invoke-DevVisualCore 'ui.observe' $observe
Assert-VisualTest ($first.observation.dpi -eq 144 -and $first.observation.rect.x -eq -1000 -and $first.observation.window_rect.x -eq -1008) 'Observation conflated actual capture bounds and window geometry.'
Assert-DevVisualToken $first.observation 5000
Assert-VisualTest ($first.observation.token.Length -gt 32) 'Observation was not protected.'
$o=Resolve-DevVisualObservation (Copy-VisualObject $first.observation)
$point=Get-DevVisualPoint $o (Resolve-DevVisualTarget ([PSCustomObject]@{x=25;y=10;image_width=100;image_height=50}) $o)
Assert-VisualTest ($point.x -eq -949 -and $point.y -eq -79) 'Scaled point mapping failed on negative-coordinate monitor.'
$point=Get-DevVisualPoint $o (Resolve-DevVisualTarget ([PSCustomObject]@{x=25;y=10;width=10;height=10;image_width=100;image_height=50}) $o)
Assert-VisualTest ($point.x -eq -940 -and $point.y -eq -70) 'Rectangle center mapping failed.'
foreach($target in @(@{x=-1;y=0},@{x=200;y=0},@{x=0;y=0;width=1},@{x=0;y=0;width=$null;height=$null},@{x=199;y=0;width=2;height=1},@{x=0;y=0;unknown=1})){
    Expect-VisualError {Resolve-DevVisualTarget ([PSCustomObject]$target) $o} 'INVALID_ARGUMENT'
}
$tampered=Copy-VisualObject $first.observation;$tampered.rect.x=-999
Expect-VisualError {Assert-DevVisualToken (Resolve-DevVisualObservation $tampered) 5000} 'OBSERVATION_INVALID'
$tampered=Copy-VisualObject $first.observation;$raw=[Convert]::FromBase64String($tampered.token);$raw[8]=$raw[8] -bxor 1;$tampered.token=[Convert]::ToBase64String($raw)
Expect-VisualError {Assert-DevVisualToken (Resolve-DevVisualObservation $tampered) 5000} 'OBSERVATION_INVALID'
$old=[ordered]@{};foreach($key in $first.observation.Keys){if($key -cne 'token'){$old[$key]=$first.observation[$key]}}
$old.timestamp=[DateTimeOffset]::UtcNow.AddSeconds(-10).ToString('o');$old.expires_at=([DateTimeOffset]::Parse($old.timestamp)).AddSeconds(30).ToString('o');$old['token']=Protect-DevVisualObservation $old
Expect-VisualError {Assert-DevVisualToken $old 5000} 'OBSERVATION_EXPIRED'
$script:SessionId=8
Expect-VisualError {Assert-DevVisualToken $first.observation 5000} 'OBSERVATION_SESSION_MISMATCH'
$script:SessionId=7
$click=[PSCustomObject]@{observation=(Copy-VisualObject $first.observation);target=[PSCustomObject]@{x=25;y=10;image_width=100;image_height=50};focus=$true;wait_ms=0;include_uia=$false}
$normalized=Resolve-DevVisualParameters 'ui.click' $click
[StarbridgeDeveloperInput.Native]::Geometry.x=-1007
Expect-VisualError {Invoke-DevVisualCore 'ui.click' $normalized} 'OBSERVATION_GEOMETRY_CHANGED'
[StarbridgeDeveloperInput.Native]::Geometry.x=-1008
[StarbridgeDeveloperVisual.Native]::CurrentDpi=192
Expect-VisualError {Invoke-DevVisualCore 'ui.click' $normalized} 'OBSERVATION_DPI_CHANGED'
[StarbridgeDeveloperVisual.Native]::CurrentDpi=144
$script:IdentityValid=$false
Expect-VisualError {Invoke-DevVisualCore 'ui.click' $normalized} 'WINDOW_IDENTITY_MISMATCH'
$script:IdentityValid=$true
Assert-VisualTest ($script:Clicks -eq 0 -and $script:Focuses -eq 0 -and $script:Leases -eq 0) 'A rejected click changed application state or leaked its lease.'
$result=Invoke-DevVisualCore 'ui.click' $normalized
Assert-VisualTest ($script:Clicks -eq 1 -and $script:Focuses -eq 1 -and $script:Leases -eq 0 -and $script:LastPoint[0] -eq -949 -and $result.verification.status -ceq 'pending' -and $result.outcome -ceq 'dispatched') 'Visual click did not execute once and leave business verification pending.'
Assert-VisualTest ($result.observation.token -cne $first.observation.token -and $script:Captures -eq 2) 'Click did not produce a new authenticated observation.'
# Strict comparison is optional: an animation/hover change blocks this mode,
# while the default flow above does not require byte-identical screen content.
$strict=Copy-VisualObject $click;$strict.strict_image_match=$true;$strict.focus=$false
$strict.observation=Copy-VisualObject $result.observation
$script:Pixels=[byte[]]@(137,80,78,71,1)
Expect-VisualError {Invoke-DevVisualCore 'ui.click' (Resolve-DevVisualParameters 'ui.click' $strict)} 'OBSERVATION_IMAGE_CHANGED'
Assert-VisualTest ($script:Clicks -eq 1) 'Strict mismatch dispatched a click.'
$script:FailPost=$true;$script:DevUiMutationBegan=$false
Expect-VisualError {Invoke-DevVisualCore 'ui.click' $normalized} 'POST_OBSERVATION_FAILED'
Assert-VisualTest ($script:DevUiMutationBegan -and $script:Clicks -eq 2 -and $script:Leases -eq 0) 'Post-capture failure lost unknown mutation semantics.'
# Exercise actual module UIA degradation against a fake bounded worker. No
# provider is loaded and its failure must not remove the successful image.
function Register-DeveloperAction {param($Name,$Description,$Mutation,$Interactive,$Handler)}
function Get-DevInteger($Object,$Name,$Default,$Min,$Max){return Get-DevUiInteger $Object $Name $Default $Min $Max}
function Get-DevBoolean($Object,$Name,$Default){return Get-DevVisualBoolean $Object $Name $Default}
function Stop-Dev($Code,$Message,$Details=$null){$script:DevFailureCode=$Code;throw $Message}
function Invoke-DevUiWorker($WorkerName,$Parameters,$TimeoutMs,$Mutation=$false,$MaxOutputBytes=4194304){
    if($WorkerName -ceq 'visual-worker.ps1'){return Copy-VisualObject $first}
    if($WorkerName -ceq 'ui-query-worker.ps1'){Stop-Dev 'UIA_TIMEOUT' 'Synthetic provider timeout.'}
    throw 'Unexpected worker.'
}
. $module
$withUia=Copy-VisualObject $identity;$withUia.include_uia=$true
$degraded=Invoke-DevVisualAction 'ui.observe' $withUia
Assert-VisualTest ($degraded.uia.status -ceq 'unavailable' -and !$degraded.uia.complete -and $degraded.uia.error_code -ceq 'UIA_TIMEOUT' -and $degraded.screenshot.png_base64 -ceq $first.screenshot.png_base64) 'A failed optional UIA provider discarded the visual fallback.'
# Real worker entry/exit checks exercise early rejection only. They cannot
# reach a capture or input API, even when the CI machine has a desktop.
$powerShell=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
foreach($case in @(
    @{request=@{action='ui.click';params=@{}};code='WRITE_NOT_ALLOWED'},
    @{request=@{action='unknown.action';params=@{}};code='INVALID_ARGUMENT'},
    @{request=@{action='ui.observe';params=@{unknown=$true}};code='INVALID_ARGUMENT'}
)){
    $encoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes((ConvertTo-Json -InputObject $case.request -Depth 8 -Compress)))
    $start=New-Object Diagnostics.ProcessStartInfo
    $start.FileName=$powerShell;$start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+(Join-Path $workers 'visual-worker.ps1')+'" -RequestBase64 '+$encoded
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=New-Object Diagnostics.Process;$process.StartInfo=$start
    try{
        $null=$process.Start();$stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
        if(!$process.WaitForExit(15000)){try{$process.Kill()}catch{};throw 'Synthetic visual entry exceeded deadline.'}
        $output=$stdout.GetAwaiter().GetResult();$diagnostic=$stderr.GetAwaiter().GetResult()
        $response=ConvertFrom-Json -InputObject $output -ErrorAction Stop
        Assert-VisualTest ($process.ExitCode -eq 1 -and $response.ok -ceq $false -and $response.error.code -ceq $case.code -and $response.error.details.outcome -ceq 'not_started' -and [string]::IsNullOrWhiteSpace($diagnostic)) 'Visual worker early rejection contract failed.'
    }finally{$process.Dispose()}
}
Write-Host ('Visual flow contract passed ({0} synthetic checks; no desktop input/capture).' -f $script:Checks)
