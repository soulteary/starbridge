function Get-DevVisualBoolean($Object,[string]$Name,[bool]$Default) {
    $value=Get-DevUiField $Object $Name $Default
    if($value -isnot [bool]){Stop-DevUi 'INVALID_ARGUMENT' 'Visual boolean fields must be booleans.'}
    return $value
}
function Get-DevVisualSessionId { return [Diagnostics.Process]::GetCurrentProcess().SessionId }
function Test-DevVisualField($Object,[string]$Name) {
    if($Object -is [Collections.IDictionary]){return $Object.Contains($Name)}
    return $null -ne $Object.PSObject.Properties[$Name]
}
function Assert-DevVisualSession {
    if(([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) -or ((Get-DevVisualSessionId) -eq 0) -or ![Environment]::UserInteractive){Stop-DevUi 'INTERACTIVE_SESSION_REQUIRED' 'Visual actions require the logged-in Windows interactive session.'}
}
function Get-DevVisualRect($Object) {
    Assert-DevUiFields $Object @('x','y','width','height')
    foreach($key in @('x','y','width','height')){if(!(Test-DevVisualField $Object $key)){Stop-DevUi 'INVALID_ARGUMENT' 'Observation geometry is incomplete.'}}
    return ,([ordered]@{x=(Get-DevUiInteger $Object 'x' 0 ([int]::MinValue) ([int]::MaxValue));y=(Get-DevUiInteger $Object 'y' 0 ([int]::MinValue) ([int]::MaxValue));width=(Get-DevUiInteger $Object 'width' 0 1 16384);height=(Get-DevUiInteger $Object 'height' 0 1 16384)})
}
function Resolve-DevVisualObservation($Object) {
    Assert-DevUiFields $Object @('token','hwnd','pid','start_time_utc','timestamp','expires_at','session_id','dpi','rect','window_rect','width','height','sha256','coordinate_space','capture_method')
    $identity=Resolve-DevUiQueryParameters ([PSCustomObject]@{hwnd=(Get-DevUiField $Object 'hwnd');pid=(Get-DevUiField $Object 'pid');start_time_utc=(Get-DevUiField $Object 'start_time_utc')})
    $result=[ordered]@{hwnd=$identity.hwnd;pid=$identity.pid;start_time_utc=$identity.start_time_utc}
    foreach($key in @('token','timestamp','expires_at','sha256','coordinate_space','capture_method')){$result[$key]=Get-DevUiString $Object $key '' 5500}
    try{$token=[Convert]::FromBase64String($result.token);if($token.Length -lt 32 -or $token.Length -gt 4096 -or [Convert]::ToBase64String($token) -cne $result.token){throw 'Token.'}}catch{Stop-DevUi 'INVALID_ARGUMENT' 'Invalid observation token.'}
    $stamp=[DateTimeOffset]::MinValue;$expires=[DateTimeOffset]::MinValue
    if(![DateTimeOffset]::TryParse($result.timestamp,[ref]$stamp) -or ![DateTimeOffset]::TryParse($result.expires_at,[ref]$expires) -or ($expires-$stamp).TotalSeconds -ne 30 -or
        $result.sha256 -cnotmatch '^[0-9a-f]{64}$' -or $result.coordinate_space -cne 'guest-screen-physical-pixels' -or $result.capture_method -cnotin @('print_window','windows_graphics_capture')){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid observation metadata.'}
    $result['session_id']=Get-DevUiInteger $Object 'session_id' 0 1 ([int]::MaxValue)
    $result['dpi']=Get-DevUiInteger $Object 'dpi' 0 48 960
    $result['width']=Get-DevUiInteger $Object 'width' 0 1 16384;$result['height']=Get-DevUiInteger $Object 'height' 0 1 16384
    if(([long]$result.width*$result.height) -gt 16777216){Stop-DevUi 'INVALID_ARGUMENT' 'Observation exceeds the image budget.'}
    $result['rect']=Get-DevVisualRect (Get-DevUiField $Object 'rect');$result['window_rect']=Get-DevVisualRect (Get-DevUiField $Object 'window_rect')
    if($result.rect.width -ne $result.width -or $result.rect.height -ne $result.height){Stop-DevUi 'INVALID_ARGUMENT' 'Observation image dimensions differ from capture geometry.'}
    return ,$result
}
function Resolve-DevVisualTarget($Object,$Observation) {
    Assert-DevUiFields $Object @('x','y','width','height','image_width','image_height')
    $iw=Get-DevUiInteger $Object 'image_width' $Observation.width 1 16384;$ih=Get-DevUiInteger $Object 'image_height' $Observation.height 1 16384
    $x=Get-DevUiInteger $Object 'x' -1 0 ($iw-1);$y=Get-DevUiInteger $Object 'y' -1 0 ($ih-1)
    if($x -lt 0 -or $y -lt 0){Stop-DevUi 'INVALID_ARGUMENT' 'A visual target requires x and y.'}
    $hasWidth=Test-DevVisualField $Object 'width';$hasHeight=Test-DevVisualField $Object 'height'
    $result=[ordered]@{x=$x;y=$y;image_width=$iw;image_height=$ih}
    if($hasWidth -ne $hasHeight){Stop-DevUi 'INVALID_ARGUMENT' 'A rectangle requires both width and height.'}
    if($hasWidth){$result['width']=Get-DevUiInteger $Object 'width' 0 1 ($iw-$x);$result['height']=Get-DevUiInteger $Object 'height' 0 1 ($ih-$y)}
    return ,$result
}
function Resolve-DevVisualParameters([string]$Name,$Object) {
    $allowed=@('include_uia','uia_timeout_ms','timeout_ms','max_bytes','max_pixels','capture_method')
    if($Name -ceq 'ui.observe'){$allowed+=@('hwnd','pid','start_time_utc')}else{$allowed+=@('observation','target','focus','button','max_age_ms','strict_image_match','wait_ms','lock_timeout_ms')}
    Assert-DevUiFields $Object $allowed
    if($Name -ceq 'ui.click'){$observation=Resolve-DevVisualObservation (Get-DevUiField $Object 'observation');$identity=$observation}else{$identity=$Object}
    $timeout=Get-DevUiInteger $Object 'timeout_ms' 15000 2000 20000
    $capture=[ordered]@{hwnd=(Get-DevUiField $identity 'hwnd');pid=(Get-DevUiField $identity 'pid');start_time_utc=(Get-DevUiField $identity 'start_time_utc');timeout_ms=$timeout}
    if($Name -ceq 'ui.click' -and $observation.capture_method -cne 'print_window'){$capture['capture_method']=$observation.capture_method}
    foreach($key in @('max_bytes','max_pixels','capture_method')){if(Test-DevVisualField $Object $key){$capture[$key]=Get-DevUiField $Object $key}}
    $result=Resolve-DevScreenshotParameters $capture
    $result['include_uia']=Get-DevVisualBoolean $Object 'include_uia' $true
    $result['uia_timeout_ms']=Get-DevUiInteger $Object 'uia_timeout_ms' 1500 1000 5000
    if($result.uia_timeout_ms -ge $timeout){Stop-DevUi 'INVALID_ARGUMENT' 'uia_timeout_ms must be smaller than timeout_ms.'}
    if($Name -ceq 'ui.click'){
        foreach($key in @('hwnd','pid','start_time_utc')){$result.Remove($key)}
        $result['observation']=$observation;$result['target']=Resolve-DevVisualTarget (Get-DevUiField $Object 'target') $observation
        $result['focus']=Get-DevVisualBoolean $Object 'focus' $false;$result['strict_image_match']=Get-DevVisualBoolean $Object 'strict_image_match' $false
        $result['button']=Get-DevUiString $Object 'button' 'left' 16
        if($result.button -cnotin @('left','right','middle')){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid click button.'}
        $result['max_age_ms']=Get-DevUiInteger $Object 'max_age_ms' 15000 100 30000
        $result['wait_ms']=Get-DevUiInteger $Object 'wait_ms' 250 0 2000;$result['lock_timeout_ms']=Get-DevUiInteger $Object 'lock_timeout_ms' 0 0 5000
        if(($result.wait_ms+$result.lock_timeout_ms) -ge $timeout){Stop-DevUi 'INVALID_ARGUMENT' 'Lock and wait budgets must be smaller than timeout_ms.'}
    }
    return ,$result
}
function Initialize-DevVisualNative {
    if('StarbridgeDeveloperVisual.Native' -as [type]){return}
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace StarbridgeDeveloperVisual {
 public static class Native {
  [DllImport("user32.dll")] static extern uint GetDpiForWindow(IntPtr hwnd);
  public static int Dpi(IntPtr hwnd) { try { return checked((int)GetDpiForWindow(hwnd)); } catch(EntryPointNotFoundException) { return 0; } }
 }
}
'@
}
function Get-DevVisualDpi([IntPtr]$Handle) {
    $dpi=[StarbridgeDeveloperVisual.Native]::Dpi($Handle)
    if($dpi -lt 48 -or $dpi -gt 960){Stop-DevUi 'DPI_CONTEXT_UNAVAILABLE' 'Visual observations require a supported window DPI.'}
    return $dpi
}
function Protect-DevVisualObservation($Observation) {
    Add-Type -AssemblyName System.Security
    $json=ConvertTo-Json -InputObject $Observation -Depth 8 -Compress
    $entropy=[Text.Encoding]::UTF8.GetBytes('Starbridge.visual-observation.v1')
    $protected=[Security.Cryptography.ProtectedData]::Protect([Text.Encoding]::UTF8.GetBytes($json),$entropy,[Security.Cryptography.DataProtectionScope]::CurrentUser)
    return [Convert]::ToBase64String($protected)
}
function Assert-DevVisualToken($Observation,[int]$MaxAgeMs) {
    Add-Type -AssemblyName System.Security
    try {
        $entropy=[Text.Encoding]::UTF8.GetBytes('Starbridge.visual-observation.v1')
        $bytes=[Security.Cryptography.ProtectedData]::Unprotect([Convert]::FromBase64String($Observation.token),$entropy,[Security.Cryptography.DataProtectionScope]::CurrentUser)
        if($bytes.Length -gt 8192){throw 'Receipt too large.'}
        $utf8=New-Object Text.UTF8Encoding($false,$true)
        $receipt=ConvertFrom-Json -InputObject ($utf8.GetString($bytes)) -ErrorAction Stop
        foreach($key in @('hwnd','pid','start_time_utc','timestamp','expires_at','session_id','dpi','width','height','sha256','coordinate_space','capture_method')){
            if([string](Get-DevUiField $receipt $key) -cne [string]$Observation[$key]){throw 'Metadata mismatch.'}
        }
        foreach($key in @('rect','window_rect')){foreach($field in @('x','y','width','height')){
            if([long](Get-DevUiField (Get-DevUiField $receipt $key) $field) -ne [long]$Observation[$key][$field]){throw 'Geometry mismatch.'}
        }}
    } catch {Stop-DevUi 'OBSERVATION_INVALID' 'The visual token does not authenticate this observation for the current Windows user.'}
    if($Observation.session_id -ne (Get-DevVisualSessionId)){Stop-DevUi 'OBSERVATION_SESSION_MISMATCH' 'The observation belongs to another Windows session.'}
    $stamp=[DateTimeOffset]::Parse($Observation.timestamp);$expires=[DateTimeOffset]::Parse($Observation.expires_at);$now=[DateTimeOffset]::UtcNow
    if($stamp -gt $now -or $now -gt $expires -or ($now-$stamp).TotalMilliseconds -gt $MaxAgeMs){Stop-DevUi 'OBSERVATION_EXPIRED' 'The image is too old; request ui.observe and locate the target again.'}
}
function Assert-DevVisualGeometry($Observation,[IntPtr]$Handle) {
    $null=Assert-DevUiWindow $Observation
    $rect=[StarbridgeDeveloperInput.Native]::WindowRect($Handle)
    if($null -eq $rect){Stop-DevUi 'WINDOW_NOT_FOUND' 'The observed window no longer exists.'}
    foreach($field in @('x','y','width','height')){if($rect.$field -ne $Observation.window_rect[$field]){Stop-DevUi 'OBSERVATION_GEOMETRY_CHANGED' 'The window moved or resized; locate the target in a new observation.'}}
    if((Get-DevVisualDpi $Handle) -ne $Observation.dpi){Stop-DevUi 'OBSERVATION_DPI_CHANGED' 'The window DPI changed; request a new observation.'}
}
function Get-DevVisualPoint($Observation,$Target) {
    $point=[ordered]@{}
    foreach($axis in @(@('x','width','image_width'),@('y','height','image_height'))){
        $extent=1;if($Target.Contains($axis[1])){$extent=$Target[$axis[1]]}
        # Pixel centers, or the rectangle center, in the model's image grid.
        $twice=[long]$Target[$axis[0]]*2+$extent
        $offset=[Math]::Floor(($twice*[long]$Observation[$axis[1]])/(2.0*$Target[$axis[2]]))
        $physical=[long]$Observation.rect[$axis[0]]+[long]$offset
        if($physical -lt -65535 -or $physical -gt 65535){Stop-DevUi 'VISUAL_TARGET_OUT_OF_BOUNDS' 'The mapped point exceeds the physical input coordinate budget.'}
        $point[$axis[0]]=[int]$physical
    }
    return ,$point
}
function New-DevVisualCapture($Parameters) {
    $handle=Assert-DevUiWindow $Parameters
    $before=[StarbridgeDeveloperInput.Native]::WindowRect($handle);$dpi=Get-DevVisualDpi $handle
    if($null -eq $before){Stop-DevUi 'WINDOW_NOT_FOUND' 'The window geometry is unavailable.'}
    $screenshot=Invoke-DevScreenshotCore $Parameters
    $after=[StarbridgeDeveloperInput.Native]::WindowRect($handle)
    if($null -eq $after -or (Get-DevVisualDpi $handle) -ne $dpi){Stop-DevUi 'WINDOW_GEOMETRY_CHANGED' 'The window DPI changed during observation.'}
    foreach($field in @('x','y','width','height')){if($before.$field -ne $after.$field){Stop-DevUi 'WINDOW_GEOMETRY_CHANGED' 'The window geometry changed during observation.'}}
    $stamp=[DateTimeOffset]::Parse($screenshot.timestamp)
    $observation=[ordered]@{hwnd=$screenshot.hwnd;pid=$screenshot.pid;start_time_utc=$screenshot.start_time_utc;timestamp=$screenshot.timestamp;expires_at=$stamp.AddSeconds(30).ToString('o');session_id=(Get-DevVisualSessionId);dpi=$dpi
        rect=$screenshot.rect;window_rect=[ordered]@{x=$after.x;y=$after.y;width=$after.width;height=$after.height};width=$screenshot.width;height=$screenshot.height;sha256=$screenshot.sha256;coordinate_space=$screenshot.coordinate_space;capture_method=$screenshot.capture_method}
    $observation['token']=Protect-DevVisualObservation $observation
    return ,([ordered]@{action='ui.observe';observation=$observation;screenshot=$screenshot;source='visual';uia=[ordered]@{status='not_requested';complete=$false}})
}
function Invoke-DevVisualCore([string]$Name,$Parameters) {
    Assert-DevVisualSession
    Initialize-DevUiNative;Initialize-DevUiInputNative;Initialize-DevVisualNative
    $previousDpi=[IntPtr]::Zero;$lease=$null
    try {
        $previousDpi=[StarbridgeDeveloperUi.Native]::EnterDpi()
        if($previousDpi -eq [IntPtr]::Zero){Stop-DevUi 'DPI_CONTEXT_UNAVAILABLE' 'Visual coordinates require a per-monitor DPI context.'}
        if($Name -ceq 'ui.observe'){return New-DevVisualCapture $Parameters}
        $lease=Enter-DevUiSessionLease $Parameters.lock_timeout_ms
        $observation=$Parameters.observation
        Assert-DevVisualToken $observation $Parameters.max_age_ms
        $handle=Assert-DevUiWindow $observation
        Assert-DevVisualGeometry $observation $handle
        $actionParams=[ordered]@{hwnd=$observation.hwnd;pid=$observation.pid;start_time_utc=$observation.start_time_utc;timeout_ms=$Parameters.timeout_ms;lock_timeout_ms=0}
        if($Parameters.focus){$null=Invoke-DevUiActionCore 'window.focus' $actionParams -SessionLeaseHeld}
        Assert-DevVisualGeometry $observation $handle
        Assert-DevVisualToken $observation $Parameters.max_age_ms
        $capture=[ordered]@{hwnd=$observation.hwnd;pid=$observation.pid;start_time_utc=$observation.start_time_utc;timeout_ms=$Parameters.timeout_ms;max_bytes=$Parameters.max_bytes;max_pixels=$Parameters.max_pixels}
        $method=Get-DevUiField $Parameters 'capture_method' $observation.capture_method
        $capture['capture_method']=$method
        if($Parameters.strict_image_match){
            $strictCapture=[ordered]@{};foreach($key in $capture.Keys){$strictCapture[$key]=$capture[$key]}
            # Match the method and pixel boundary used to locate this candidate.
            $strictCapture['capture_method']=$observation.capture_method
            $image=Invoke-DevScreenshotCore $strictCapture
            if($image.sha256 -cne $observation.sha256){Stop-DevUi 'OBSERVATION_IMAGE_CHANGED' 'The rendered image changed; locate the candidate in a new observation.'}
        }
        $point=Get-DevVisualPoint $observation $Parameters.target
        $actionParams['event']='click';$actionParams['button']=$Parameters.button;$actionParams['x']=$point.x;$actionParams['y']=$point.y
        # The core rechecks foreground, client bounds, and covering windows. One
        # outer lease covers focus, validation, input and the resulting capture.
        Assert-DevVisualGeometry $observation $handle;Assert-DevVisualToken $observation $Parameters.max_age_ms
        $null=Invoke-DevUiActionCore 'input.mouse' $actionParams -SessionLeaseHeld
        if($Parameters.wait_ms -gt 0){Start-Sleep -Milliseconds $Parameters.wait_ms}
        try{$data=New-DevVisualCapture $capture}catch{Stop-DevUi 'POST_OBSERVATION_FAILED' 'Input may have been dispatched, but its resulting image is unavailable. Inspect state before retrying.'}
        $data['action']='ui.click';$data['outcome']='dispatched';$data['coordination']='windows-session-mutex';$data['input']=[ordered]@{x=$point.x;y=$point.y;coordinate_space='guest-screen-physical-pixels'};$data['verification']=[ordered]@{status='pending'}
        return ,$data
    } finally {
        [StarbridgeDeveloperUi.Native]::LeaveDpi($previousDpi)
        if($null -ne $lease){try{$lease.ReleaseMutex()}finally{$lease.Dispose()}}
    }
}
