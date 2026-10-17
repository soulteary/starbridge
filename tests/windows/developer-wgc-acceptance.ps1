#requires -Version 5.1
<# Explicit opt-in only. Shows and captures this script's own synthetic form;
never accepts an arbitrary HWND, desktop region, VM, output path or app name. #>
[CmdletBinding()]
param([switch]$EnableCapture,[string]$HelperPath=(Join-Path $PSScriptRoot '..\..\guest\windows\developer-bridge.ps1'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
if(!$EnableCapture){Write-Host 'SKIP: real WGC fixture capture requires -EnableCapture.';exit 0}
if(![Environment]::UserInteractive -or [Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0){throw 'WGC fixture capture requires an interactive disposable Windows session.'}
Add-Type -AssemblyName System.Windows.Forms,System.Drawing
$form=New-Object Windows.Forms.Form
$form.Text='Starbridge synthetic WGC fixture';$form.ClientSize=New-Object Drawing.Size(320,180)
$form.BackColor=[Drawing.Color]::FromArgb(20,70,180);$form.StartPosition='CenterScreen'
$label=New-Object Windows.Forms.Label;$label.Text='Synthetic capture fixture';$label.ForeColor=[Drawing.Color]::White;$label.AutoSize=$true;$label.Location=New-Object Drawing.Point(20,20);$form.Controls.Add($label)
$child=$null;$image=$null;$stream=$null
$phase='setup';$safeCode='none'
try{
    $form.Show();[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 250
    $owner=[Diagnostics.Process]::GetCurrentProcess()
    try{$parameters=@{hwnd=('0x{0:x}' -f $form.Handle.ToInt64());pid=$owner.Id;start_time_utc=$owner.StartTime.ToUniversalTime().ToString('o');capture_method='windows_graphics_capture';timeout_ms=20000;max_pixels=1048576;max_bytes=1048576}}finally{$owner.Dispose()}
    $json=ConvertTo-Json -InputObject @{action='screenshot.capture';params=$parameters} -Depth 8 -Compress
    $request=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($json))
    $helper=(Resolve-Path -LiteralPath $HelperPath).ProviderPath
    $command="& '"+$helper.Replace("'","''")+"' -RequestBase64 '"+$request+"'"
    $start=New-Object Diagnostics.ProcessStartInfo
    $start.FileName=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $start.Arguments='-NoLogo -NoProfile -NonInteractive -Mta -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $child=New-Object Diagnostics.Process;$child.StartInfo=$start;$null=$child.Start()
    $phase='capture'
    $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync();$clock=[Diagnostics.Stopwatch]::StartNew()
    while(!$child.WaitForExit(20)){
        [Windows.Forms.Application]::DoEvents()
        if($clock.ElapsedMilliseconds -gt 30000){try{$child.Kill()}catch{};throw 'WGC synthetic capture exceeded its fixture deadline.'}
    }
    $response=ConvertFrom-Json -InputObject $stdout.GetAwaiter().GetResult()
    $diagnostics=$stderr.GetAwaiter().GetResult()
    if(!$response.ok){
        $code='UNKNOWN';if($response.error.code -is [string] -and $response.error.code -cmatch '^[A-Z_]{1,80}$'){$code=$response.error.code}
        $safeCode=$code;throw 'WGC synthetic capture failed.'
    }
    if($child.ExitCode -ne 0 -or ![string]::IsNullOrWhiteSpace($diagnostics) -or $response.data.capture_method -cne 'windows_graphics_capture'){throw 'WGC fixture response contract failed.'}
    $png=[Convert]::FromBase64String($response.data.png_base64);$stream=New-Object IO.MemoryStream(,$png);$image=New-Object Drawing.Bitmap($stream)
    $phase='known pixels'
    if($image.Width -ne $response.data.width -or $image.Height -ne $response.data.height){throw 'WGC fixture PNG dimensions changed.'}
    # A complete PNG alone is insufficient: check the known synthetic interior.
    $pixel=$image.GetPixel([int]($image.Width/2),[int]($image.Height/2))
    if([Math]::Abs([int]$pixel.R-20) -gt 3 -or [Math]::Abs([int]$pixel.G-70) -gt 3 -or [Math]::Abs([int]$pixel.B-180) -gt 3){throw 'WGC fixture did not retain its known synthetic pixels.'}
    Write-Host 'PASS: explicit WGC capture of the synthetic fixture retained its known pixels; arbitrary desktop compatibility is unverified.'
}catch{
    # All assertions above use fixed messages/codes; never print interop errors,
    # image data, request contents, runtime paths, or environment values.
    [Console]::Error.WriteLine(('WGC opt-in fixture failed: phase={0}; code={1}.' -f $phase,$safeCode));exit 1
}finally{
    if($null -ne $image){$image.Dispose()};if($null -ne $stream){$stream.Dispose()}
    if($null -ne $child){$child.Dispose()};$form.Close();$form.Dispose()
}
