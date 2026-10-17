function Resolve-DevScreenshotParameters($Object) {
    Assert-DevUiFields $Object @('hwnd', 'pid', 'start_time_utc', 'timeout_ms', 'max_bytes', 'max_pixels', 'capture_method')
    $identity = [PSCustomObject]@{
        hwnd=(Get-DevUiField $Object 'hwnd');pid=(Get-DevUiField $Object 'pid');start_time_utc=(Get-DevUiField $Object 'start_time_utc')
    }
    $query = Resolve-DevUiQueryParameters $identity
    $method=Get-DevUiString $Object 'capture_method' 'print_window' 32
    if($method -cnotin @('print_window','windows_graphics_capture')){Stop-DevUi 'INVALID_ARGUMENT' 'capture_method must be print_window or windows_graphics_capture.'}
    return ,([ordered]@{
        hwnd=$query.hwnd;pid=$query.pid;start_time_utc=$query.start_time_utc
        timeout_ms=(Get-DevUiInteger $Object 'timeout_ms' 10000 1000 20000)
        max_bytes=(Get-DevUiInteger $Object 'max_bytes' 4194304 1024 4194304)
        max_pixels=(Get-DevUiInteger $Object 'max_pixels' 8388608 1 16777216)
        capture_method=$method
    })
}

function Assert-DevScreenshotContext($Parameters,[IntPtr]$Handle) {
    $process=$null
    try {
        $process=[Diagnostics.Process]::GetProcessById($Parameters.pid)
        if($process.SessionId -ne [Diagnostics.Process]::GetCurrentProcess().SessionId) {
            Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The requested window belongs to a different Windows session.'
        }
    } finally { if($null -ne $process){$process.Dispose()} }
    if(![StarbridgeDeveloperInput.Native]::InputDesktopAccessible($Handle)) {
        Stop-DevUi 'INPUT_DESKTOP_UNAVAILABLE' 'Window capture requires the caller and target to share the accessible input desktop.'
    }
    if(![StarbridgeDeveloperInput.Native]::IsWindowVisible($Handle) -or [StarbridgeDeveloperInput.Native]::IsIconic($Handle)) {
        Stop-DevUi 'WINDOW_NOT_CAPTURABLE' 'Window capture requires a visible, non-minimized window; it will not activate or restore it.'
    }
}

function ConvertTo-DevScreenshotResult($Parameters,$Rect,[byte[]]$Png,[DateTime]$Started,[DateTime]$Finished) {
    if($Png.Length -lt 1 -or $Png.Length -gt $Parameters.max_bytes) { Stop-DevUi 'SCREENSHOT_BYTE_LIMIT' 'The rendered PNG exceeded max_bytes.' }
    $hash=[Security.Cryptography.SHA256]::Create()
    try { $digest=([BitConverter]::ToString($hash.ComputeHash($Png))).Replace('-','').ToLowerInvariant() }
    finally { $hash.Dispose() }
    return ,([ordered]@{
        hwnd=$Parameters.hwnd;pid=$Parameters.pid;start_time_utc=$Parameters.start_time_utc
        timestamp=$Finished.ToUniversalTime().ToString('o');capture_started_at=$Started.ToUniversalTime().ToString('o')
        coordinate_space='guest-screen-physical-pixels'
        rect=[ordered]@{x=$Rect.x;y=$Rect.y;width=$Rect.width;height=$Rect.height}
        width=$Rect.width;height=$Rect.height;png_base64=[Convert]::ToBase64String($Png)
        sha256=$digest;byte_length=$Png.Length;capture_method=$Parameters.capture_method
        warnings=@(if($Parameters.capture_method -ceq 'print_window'){'application-rendered-best-effort'}else{'compositor-frame-best-effort';'bgra8-hdr-may-clip';'system-capture-border-may-appear'})
    })
}

function Invoke-DevScreenshotCore($Parameters) {
    $clock=[Diagnostics.Stopwatch]::StartNew()
    if(([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) -or ([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) -or ![Environment]::UserInteractive) {
        Stop-DevUi 'INTERACTIVE_SESSION_REQUIRED' 'Window capture requires the logged-in Windows interactive session.'
    }
    Initialize-DevUiNative;Initialize-DevUiInputNative;Initialize-DevScreenshotNative
    $previousDpi=[IntPtr]::Zero
    try {
        $handle=Assert-DevUiWindow $Parameters
        Assert-DevScreenshotContext $Parameters $handle
        $previousDpi=[StarbridgeDeveloperUi.Native]::EnterDpi()
        if($previousDpi -eq [IntPtr]::Zero) { Stop-DevUi 'DPI_CONTEXT_UNAVAILABLE' 'Window capture requires a per-monitor DPI context for physical-pixel geometry.' }
        $before=Get-DevScreenshotRect $Parameters $handle
        if($null -eq $before){Stop-DevUi 'WINDOW_NOT_FOUND' 'The window geometry is unavailable.'}
        $started=[DateTime]::UtcNow
        try {
            if($Parameters.capture_method -ceq 'windows_graphics_capture'){
                $remaining=[int]($Parameters.timeout_ms-$clock.ElapsedMilliseconds)
                if($remaining -le 0){Stop-DevUi 'SCREENSHOT_TIMEOUT' 'The screenshot exceeded timeout_ms.'}
                $png=[StarbridgeDeveloperScreenshot.Wgc]::Capture($handle,$before.width,$before.height,$Parameters.max_pixels,$Parameters.max_bytes,$remaining)
            }else{
                $png=[StarbridgeDeveloperScreenshot.Native]::Capture($handle,$before.width,$before.height,$Parameters.max_pixels,$Parameters.max_bytes)
            }
        } catch {
            $cause=$_.Exception
            while($null -ne $cause.InnerException){$cause=$cause.InnerException}
            if($cause -is [StarbridgeDeveloperScreenshot.CaptureException]) {
                switch -CaseSensitive($cause.Code) {
                    'SCREENSHOT_PIXEL_LIMIT'{Stop-DevUi 'SCREENSHOT_PIXEL_LIMIT' 'The window dimensions exceeded max_pixels or the 16384-pixel dimension limit.'}
                    'SCREENSHOT_BYTE_LIMIT'{Stop-DevUi 'SCREENSHOT_BYTE_LIMIT' 'The rendered PNG exceeded max_bytes.'}
                    'SCREENSHOT_RENDER_FAILED'{Stop-DevUi 'SCREENSHOT_RENDER_FAILED' 'The target application did not provide a window rendering.'}
                    'SCREENSHOT_TIMEOUT'{Stop-DevUi 'SCREENSHOT_TIMEOUT' 'The screenshot exceeded timeout_ms.'}
                    'WINDOW_GEOMETRY_CHANGED'{Stop-DevUi 'WINDOW_GEOMETRY_CHANGED' 'The window capture size changed; request a new observation.'}
                    'WGC_OS_UNSUPPORTED'{Stop-DevUi $cause.Code 'Windows Graphics Capture requires Windows 10 build 18362 or later.'}
                    'WGC_API_UNAVAILABLE'{Stop-DevUi $cause.Code 'The required Windows Graphics Capture interfaces are unavailable.'}
                    'WGC_UNSUPPORTED'{Stop-DevUi $cause.Code 'Windows Graphics Capture is unsupported by this device or system policy.'}
                    'WGC_ACCESS_DENIED'{Stop-DevUi $cause.Code 'Windows denied access to this window capture; no alternate backend was attempted.'}
                    'WGC_WINDOW_UNSUPPORTED'{Stop-DevUi $cause.Code 'Windows Graphics Capture cannot capture this window.'}
                    'WGC_WINDOW_EXCLUDED'{Stop-DevUi $cause.Code 'The target window excludes capture through its display-affinity policy.'}
                    'WGC_DEVICE_UNAVAILABLE'{Stop-DevUi $cause.Code 'A hardware Direct3D 11 capture device is unavailable.'}
                    'WGC_DEVICE_LOST'{Stop-DevUi $cause.Code 'The graphics device was removed or reset during capture.'}
                    'WGC_FRAME_UNAVAILABLE'{Stop-DevUi $cause.Code 'Windows Graphics Capture could not provide a frame.'}
                    'WGC_READBACK_FAILED'{Stop-DevUi $cause.Code 'The graphics frame could not be read within the capture budget.'}
                    'WGC_INVALID_BUFFER'{Stop-DevUi $cause.Code 'The graphics frame did not have the expected bounded BGRA8 layout.'}
                }
            }
            Stop-DevUi 'SCREENSHOT_ENCODE_FAILED' 'The window rendering could not be encoded within the capture budget.'
        }
        $finished=[DateTime]::UtcNow
        $null=Assert-DevUiWindow $Parameters
        Assert-DevScreenshotContext $Parameters $handle
        $after=Get-DevScreenshotRect $Parameters $handle
        if($null -eq $after -or $before.x -ne $after.x -or $before.y -ne $after.y -or $before.width -ne $after.width -or $before.height -ne $after.height) {
            Stop-DevUi 'WINDOW_GEOMETRY_CHANGED' 'The window moved or resized during capture; request a new observation.'
        }
        return ConvertTo-DevScreenshotResult $Parameters $before $png $started $finished
    } finally { [StarbridgeDeveloperUi.Native]::LeaveDpi($previousDpi) }
}

function Get-DevScreenshotRect($Parameters,[IntPtr]$Handle) {
    if($Parameters.capture_method -ceq 'print_window'){return [StarbridgeDeveloperInput.Native]::WindowRect($Handle)}
    try{return [StarbridgeDeveloperScreenshot.Wgc]::WindowRect($Handle)}
    catch{Stop-DevUi 'WGC_GEOMETRY_UNAVAILABLE' 'The compositor window bounds are unavailable; no capture was attempted.'}
}
