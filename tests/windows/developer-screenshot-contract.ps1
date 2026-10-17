#requires -Version 5.1
<# Compile native helpers and encode generated pixels only. This suite does not
capture real windows, start applications, or interact with a virtual machine. #>
[CmdletBinding()]
param([string]$Root=(Join-Path $PSScriptRoot '..\..'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop'
$rootPath=(Resolve-Path -LiteralPath $Root).ProviderPath
$workers=Join-Path $rootPath 'guest\windows\developer\workers'
foreach($file in @('screenshot-core.ps1','screenshot-native.ps1','screenshot-worker.ps1')) {
    $tokens=$null;$errors=$null
    $null=[Management.Automation.Language.Parser]::ParseFile((Join-Path $workers $file),[ref]$tokens,[ref]$errors)
    if($errors.Count -ne 0){throw ('Screenshot syntax error: '+$file)}
}
. (Join-Path $workers 'ui-core.ps1')
. (Join-Path $workers 'ui-input-native.ps1')
. (Join-Path $workers 'screenshot-native.ps1')
. (Join-Path $workers 'screenshot-core.ps1')
$checks=0
function New-CaptureParameters {
    return [PSCustomObject]@{hwnd='0X000123';pid=42;start_time_utc='2026-01-02T03:04:05.1234567Z'}
}
$parameters=Resolve-DevScreenshotParameters (New-CaptureParameters)
if($parameters.hwnd -cne '0x123' -or $parameters.max_bytes -ne 4194304 -or $parameters.max_pixels -ne 8388608){throw 'Capture defaults or identity were not normalized.'}
$checks++
foreach($case in @(
    @{key='path';value='fixture.png'},@{key='hwnd';value=123},@{key='pid';value=0},
    @{key='max_bytes';value=1023},@{key='max_bytes';value=4194305},
    @{key='max_pixels';value=0},@{key='max_pixels';value=16777217},@{key='max_pixels';value=$null},
    @{key='timeout_ms';value=999},@{key='timeout_ms';value=20001}
)) {
    $p=New-CaptureParameters;$p|Add-Member NoteProperty $case.key $case.value -Force
    $script:DevUiErrorCode=''
    try{$null=Resolve-DevScreenshotParameters $p;throw 'Invalid screenshot parameters were accepted.'}
    catch{if($script:DevUiErrorCode -cne 'INVALID_ARGUMENT'){throw}}
    $checks++
}
Initialize-DevUiNative;Initialize-DevUiInputNative;Initialize-DevScreenshotNative
foreach($dimensions in @(@(0,1,100),@(1,0,100),@(16385,1,16777216),@(100,100,9999))) {
    try{[StarbridgeDeveloperScreenshot.Native]::ValidateDimensions($dimensions[0],$dimensions[1],$dimensions[2]);throw 'Pixel budget was ignored.'}
    catch{
        $cause=$_.Exception;while($null -ne $cause.InnerException){$cause=$cause.InnerException}
        if($cause -isnot [StarbridgeDeveloperScreenshot.CaptureException] -or $cause.Code -cne 'SCREENSHOT_PIXEL_LIMIT'){throw}
    }
    $checks++
}
$bitmap=$null
try {
    $bitmap=New-Object Drawing.Bitmap(64,64)
    $random=New-Object Random(12345)
    for($y=0;$y -lt 64;$y++){for($x=0;$x -lt 64;$x++){$bitmap.SetPixel($x,$y,[Drawing.Color]::FromArgb($random.Next(256),$random.Next(256),$random.Next(256)))}}
    $png=[StarbridgeDeveloperScreenshot.Native]::Encode($bitmap,1048576)
    if($png.Length -le 1024){throw 'Generated fixture should exercise the compressed byte budget.'}
    $stream=New-Object IO.MemoryStream(,$png)
    $image=$null
    try{
        $image=[Drawing.Image]::FromStream($stream)
        if($image.Width -ne 64 -or $image.Height -ne 64 -or $image.RawFormat.Guid -ne [Drawing.Imaging.ImageFormat]::Png.Guid){throw 'Fixture was not a complete PNG.'}
    }finally{if($null -ne $image){$image.Dispose()};$stream.Dispose()}
    $checks++
    try{$null=[StarbridgeDeveloperScreenshot.Native]::Encode($bitmap,1024);throw 'Compressed byte budget was ignored.'}
    catch{
        $cause=$_.Exception;while($null -ne $cause.InnerException){$cause=$cause.InnerException}
        if($cause -isnot [StarbridgeDeveloperScreenshot.CaptureException] -or $cause.Code -cne 'SCREENSHOT_BYTE_LIMIT'){throw}
    }
    $checks++
    $rect=[PSCustomObject]@{x=-100;y=25;width=64;height=64}
    $started=[DateTime]::Parse('2026-01-02T03:05:00Z').ToUniversalTime()
    $finished=$started.AddMilliseconds(100)
    $result=ConvertTo-DevScreenshotResult $parameters $rect $png $started $finished
    if($result.hwnd -cne '0x123' -or $result.pid -ne 42 -or $result.rect.x -ne -100 -or $result.width -ne 64 -or $result.height -ne 64 -or
        $result.byte_length -ne $png.Length -or $result.coordinate_space -cne 'guest-screen-physical-pixels' -or $result.capture_method -cne 'print_window' -or
        $result.warnings.Count -ne 1 -or $result.warnings[0] -cne 'application-rendered-best-effort' -or $result.sha256 -cnotmatch '^[0-9a-f]{64}$') {throw 'Capture result lost its identity, geometry, or rendering caveat.'}
    $hash=[Security.Cryptography.SHA256]::Create()
    try{$expected=([BitConverter]::ToString($hash.ComputeHash([Convert]::FromBase64String($result.png_base64)))).Replace('-','').ToLowerInvariant()}
    finally{$hash.Dispose()}
    if($expected -cne $result.sha256){throw 'Capture hash does not cover the returned PNG.'}
    $checks++
} finally {if($null -ne $bitmap){$bitmap.Dispose()}}
Write-Host ('Screenshot contract passed ({0} synthetic checks; no real window was captured).' -f $checks)
