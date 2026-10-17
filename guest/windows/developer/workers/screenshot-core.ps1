function Resolve-DevScreenshotParameters($Object) {
    Assert-DevUiFields $Object @('hwnd', 'pid', 'start_time_utc', 'timeout_ms', 'max_bytes', 'max_pixels')
    $identity = [PSCustomObject]@{
        hwnd=(Get-DevUiField $Object 'hwnd');pid=(Get-DevUiField $Object 'pid');start_time_utc=(Get-DevUiField $Object 'start_time_utc')
    }
    $query = Resolve-DevUiQueryParameters $identity
    return ,([ordered]@{
        hwnd=$query.hwnd;pid=$query.pid;start_time_utc=$query.start_time_utc
        timeout_ms=(Get-DevUiInteger $Object 'timeout_ms' 10000 1000 20000)
        max_bytes=(Get-DevUiInteger $Object 'max_bytes' 4194304 1024 4194304)
        max_pixels=(Get-DevUiInteger $Object 'max_pixels' 8388608 1 16777216)
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
        sha256=$digest;byte_length=$Png.Length;capture_method='print_window'
        warnings=@('application-rendered-best-effort')
    })
}

function Invoke-DevScreenshotCore($Parameters) {
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
        $before=[StarbridgeDeveloperInput.Native]::WindowRect($handle)
        if($null -eq $before){Stop-DevUi 'WINDOW_NOT_FOUND' 'The window geometry is unavailable.'}
        $started=[DateTime]::UtcNow
        try {
            $png=[StarbridgeDeveloperScreenshot.Native]::Capture($handle,$before.width,$before.height,$Parameters.max_pixels,$Parameters.max_bytes)
        } catch {
            $cause=$_.Exception
            while($null -ne $cause.InnerException){$cause=$cause.InnerException}
            if($cause -is [StarbridgeDeveloperScreenshot.CaptureException]) {
                switch -CaseSensitive($cause.Code) {
                    'SCREENSHOT_PIXEL_LIMIT'{Stop-DevUi 'SCREENSHOT_PIXEL_LIMIT' 'The window dimensions exceeded max_pixels or the 16384-pixel dimension limit.'}
                    'SCREENSHOT_BYTE_LIMIT'{Stop-DevUi 'SCREENSHOT_BYTE_LIMIT' 'The rendered PNG exceeded max_bytes.'}
                    'SCREENSHOT_RENDER_FAILED'{Stop-DevUi 'SCREENSHOT_RENDER_FAILED' 'The target application did not provide a window rendering.'}
                }
            }
            Stop-DevUi 'SCREENSHOT_ENCODE_FAILED' 'The window rendering could not be encoded within the capture budget.'
        }
        $finished=[DateTime]::UtcNow
        $null=Assert-DevUiWindow $Parameters
        Assert-DevScreenshotContext $Parameters $handle
        $after=[StarbridgeDeveloperInput.Native]::WindowRect($handle)
        if($null -eq $after -or $before.x -ne $after.x -or $before.y -ne $after.y -or $before.width -ne $after.width -or $before.height -ne $after.height) {
            Stop-DevUi 'WINDOW_GEOMETRY_CHANGED' 'The window moved or resized during capture; request a new observation.'
        }
        return ConvertTo-DevScreenshotResult $Parameters $before $png $started $finished
    } finally { [StarbridgeDeveloperUi.Native]::LeaveDpi($previousDpi) }
}
