#requires -Version 5.1
<# Compile the real COM/D3D ABI adapter, probe capability without creating a
capture item, and encode anonymous generated BGRA buffers. No window capture. #>
[CmdletBinding()]
param([string]$Root=(Join-Path $PSScriptRoot '..\..'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$workers=Join-Path (Resolve-Path -LiteralPath $Root).ProviderPath 'guest\windows\developer\workers'
foreach($file in @('screenshot-native.ps1','screenshot-core.ps1','screenshot-capabilities-worker.ps1')){
    $tokens=$null;$errors=$null
    $null=[Management.Automation.Language.Parser]::ParseFile((Join-Path $workers $file),[ref]$tokens,[ref]$errors)
    if($errors.Count -ne 0){throw 'WGC helper syntax is invalid.'}
}
. (Join-Path $workers 'ui-core.ps1')
. (Join-Path $workers 'screenshot-native.ps1')
. (Join-Path $workers 'screenshot-core.ps1')
Initialize-DevScreenshotNative
$checks=0
foreach($method in @('print_window','windows_graphics_capture')){
    $p=Resolve-DevScreenshotParameters ([PSCustomObject]@{hwnd='0x123';pid=42;start_time_utc='2026-01-02T03:04:05Z';capture_method=$method})
    if($p.capture_method -cne $method){throw 'Explicit screenshot backend was changed.'};$checks++
}
foreach($method in @('auto','desktop','Print_Window','',$null,42)){
    $script:DevUiErrorCode=''
    try{$null=Resolve-DevScreenshotParameters ([PSCustomObject]@{hwnd='0x123';pid=42;start_time_utc='2026-01-02T03:04:05Z';capture_method=$method});throw 'Invalid backend was accepted.'}
    catch{if($script:DevUiErrorCode -cne 'INVALID_ARGUMENT'){throw}};$checks++
}
# Padded source rows detect accidentally copying GPU pitch as image width.
$pixels=[byte[]]@(0,0,255,255, 0,255,0,255, 99,99,99,99, 255,0,0,255, 255,255,255,255, 88,88,88,88)
$pointer=[Runtime.InteropServices.Marshal]::AllocHGlobal($pixels.Length)
try{
    [Runtime.InteropServices.Marshal]::Copy($pixels,0,$pointer,$pixels.Length)
    $png=[StarbridgeDeveloperScreenshot.Wgc]::EncodeBgra($pointer,12,2,2,4,4096)
    $stream=New-Object IO.MemoryStream(,$png);$image=$null
    try{
        $image=New-Object Drawing.Bitmap($stream)
        if($image.Width -ne 2 -or $image.Height -ne 2 -or $image.GetPixel(0,0).R -ne 255 -or $image.GetPixel(1,0).G -ne 255 -or $image.GetPixel(0,1).B -ne 255 -or $image.GetPixel(1,1).R -ne 255){throw 'BGRA row-pitch or channel conversion changed.'}
    }finally{if($null -ne $image){$image.Dispose()};$stream.Dispose()};$checks++
    foreach($case in @(@{pitch=7;maxPixels=4;maxBytes=4096;code='WGC_INVALID_BUFFER'},@{pitch=12;maxPixels=3;maxBytes=4096;code='SCREENSHOT_PIXEL_LIMIT'},@{pitch=12;maxPixels=4;maxBytes=1;code='SCREENSHOT_BYTE_LIMIT'})){
        try{$null=[StarbridgeDeveloperScreenshot.Wgc]::EncodeBgra($pointer,$case.pitch,2,2,$case.maxPixels,$case.maxBytes);throw 'BGRA budget was ignored.'}
        catch{
            $cause=$_.Exception;while($null -ne $cause.InnerException){$cause=$cause.InnerException}
            if($cause -isnot [StarbridgeDeveloperScreenshot.CaptureException] -or $cause.Code -cne $case.code){throw 'BGRA failure did not return its safe code.'}
        };$checks++
    }
    $p=Resolve-DevScreenshotParameters ([PSCustomObject]@{hwnd='0x123';pid=42;start_time_utc='2026-01-02T03:04:05Z';capture_method='windows_graphics_capture'})
    $now=[DateTime]::UtcNow;$result=ConvertTo-DevScreenshotResult $p ([PSCustomObject]@{x=-10;y=20;width=2;height=2}) $png $now $now
    if($result.capture_method -cne 'windows_graphics_capture' -or $result.warnings.Count -ne 3 -or $result.warnings[0] -cne 'compositor-frame-best-effort' -or $result.rect.x -ne -10){throw 'WGC result lost backend, caveats or physical origin.'};$checks++
}finally{[Runtime.InteropServices.Marshal]::FreeHGlobal($pointer)}
# Probe runs the actual WinRT/D3D declarations even in a noninteractive CI job.
# Lack of a supported GPU/API is a valid detected result, never a capture pass.
$probe=[StarbridgeDeveloperScreenshot.Wgc]::Probe()
$reasons=@('WGC_OS_UNSUPPORTED','WGC_API_UNAVAILABLE','WGC_UNSUPPORTED','WGC_ACCESS_DENIED','WGC_DEVICE_UNAVAILABLE','WGC_DEVICE_LOST')
if($probe.os_build -lt 0 -or ($probe.available -and $probe.reason -cne 'available') -or (!$probe.available -and $probe.reason -cnotin $reasons)){throw 'WGC capability probe returned an invalid safe result.'};$checks++
Add-Type -Path (Join-Path $PSScriptRoot 'fixtures\wgc-d3d-fixture.cs') -ReferencedAssemblies @('System.dll')
try{
    $gpuPng=[SyntheticWgcD3D]::Run([StarbridgeDeveloperScreenshot.Wgc])
    $gpuStream=New-Object IO.MemoryStream(,$gpuPng);$gpuImage=$null
    try{
        $gpuImage=New-Object Drawing.Bitmap($gpuStream)
        if($gpuImage.Width -ne 2 -or $gpuImage.Height -ne 2 -or $gpuImage.GetPixel(0,0).R -ne 255 -or $gpuImage.GetPixel(1,0).G -ne 255 -or $gpuImage.GetPixel(0,1).B -ne 255){throw 'Generated D3D pixels changed during readback.'}
    }finally{if($null -ne $gpuImage){$gpuImage.Dispose()};$gpuStream.Dispose()}
}catch{throw 'Generated D3D resource/readback contract failed; no real window was captured.'};$checks++
Write-Host ('WGC contract passed ({0} checks; real COM/D3D ABI compiled and probed, WARP resource roundtrip and generated buffers only; no window captured).' -f $checks)
