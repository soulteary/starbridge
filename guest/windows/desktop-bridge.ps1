#requires -Version 5.1
<#
Read-only Windows guest bridge. Run in the desktop user's interactive session.
Input: -RequestBase64 <base64(UTF-8 JSON {action,params})>
Output: exactly one UTF-8 JSON response on stdout; no remote command execution.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RequestBase64,
    [switch]$UiWorker
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$script:FailureCode = 'INTERNAL_ERROR'
$script:FailureMessage = 'The guest bridge could not complete the request.'
$script:StartedAt = [DateTime]::UtcNow

function Stop-Bridge([string]$Code, [string]$Message) {
    $script:FailureCode = $Code
    $script:FailureMessage = $Message
    throw $Message
}

function Get-Field($Object, [string]$Name, $Default = $null) {
    if ($null -eq $Object) { return ,$Default }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return ,$Default }
    # Keep [] and [value] as arrays so field validation sees their JSON types.
    return ,$property.Value
}

function Test-JsonObject($Value) {
    # The [pscustomobject] accelerator aliases PSObject, including wrapped values
    # and AutomationNull. Compare the actual JSON object's underlying type.
    return (($null -ne $Value) -and ($Value.GetType() -eq [System.Management.Automation.PSCustomObject]))
}

function Get-Integer($Object, [string]$Name, [int]$Default, [int]$Min, [int]$Max) {
    $value = Get-Field $Object $Name $Default
    if (($value -isnot [int]) -and ($value -isnot [long])) {
        Stop-Bridge 'INVALID_REQUEST' "$Name must be an integer."
    }
    if (($value -lt $Min) -or ($value -gt $Max)) {
        Stop-Bridge 'INVALID_REQUEST' "$Name must be between $Min and $Max."
    }
    return [int]$value
}

function Get-Boolean($Object, [string]$Name, [bool]$Default) {
    $value = Get-Field $Object $Name $Default
    if ($value -isnot [bool]) { Stop-Bridge 'INVALID_REQUEST' "$Name must be a boolean." }
    return $value
}

function Get-CodePointLength([string]$Text) {
    $count = 0
    for ($index = 0; $index -lt $Text.Length; $index++) {
        if ([char]::IsSurrogatePair($Text, $index)) { $index++ }
        $count++
    }
    return $count
}

function Limit-Text($Value, [int]$Limit = 512) {
    if ($null -eq $Value) { return $null }
    $text = [string]$Value
    $offset = 0
    $count = 0
    while (($offset -lt $text.Length) -and ($count -lt $Limit)) {
        if ([char]::IsSurrogatePair($text, $offset)) { $offset += 2 } else { $offset++ }
        $count++
    }
    return $text.Substring(0, $offset)
}

function Write-Response($Response) {
    $json = ConvertTo-Json -InputObject $Response -Depth 24 -Compress
    [Console]::Out.WriteLine($json)
}

function New-Metadata([string]$Action, $Data) {
    $desktop = $null
    if ('Starbridge.Native' -as [type]) {
        try { $desktop = [Starbridge.Native]::GetDesktopName() } catch { }
    }
    $coordinateSpace = $null
    if ($Data -is [System.Collections.IDictionary]) { $coordinateSpace = $Data['coordinate_space'] }
    return [ordered]@{
        protocol_version = '1'
        action = $Action
        timestamp = [DateTime]::UtcNow.ToString('o')
        duration_ms = [int]([DateTime]::UtcNow - $script:StartedAt).TotalMilliseconds
        user = [Environment]::UserDomainName + '\' + [Environment]::UserName
        session_id = [Diagnostics.Process]::GetCurrentProcess().SessionId
        interactive = [Environment]::UserInteractive
        user_interactive = [Environment]::UserInteractive
        desktop = $desktop
        coordinate_space = $coordinateSpace
        read_only = $true
    }
}

function Initialize-Native {
    if ('Starbridge.Native' -as [type]) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;

namespace Starbridge {
    public sealed class Rect {
        public int x, y, width, height;
    }
    public sealed class Window {
        public string hwnd, title, class_name, process_name;
        public long pid;
        public bool is_visible;
        public Rect rect;
    }
    public sealed class WindowList {
        public List<Window> windows = new List<Window>();
        public bool truncated;
    }
    public sealed class DpiState {
        public string mode = "unknown";
        public bool per_monitor_aware;
        public IntPtr previous_context;
    }
    public static class Native {
        [StructLayout(LayoutKind.Sequential)]
        private struct RECT { public int Left, Top, Right, Bottom; }
        private delegate bool EnumWindowsProc(IntPtr hwnd, IntPtr data);
        [DllImport("user32.dll", SetLastError=true)]
        private static extern bool EnumWindows(EnumWindowsProc callback, IntPtr data);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)]
        private static extern int GetWindowText(IntPtr hwnd, StringBuilder text, int count);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)]
        private static extern int GetClassName(IntPtr hwnd, StringBuilder text, int count);
        [DllImport("user32.dll", SetLastError=true)]
        private static extern bool GetWindowRect(IntPtr hwnd, out RECT rect);
        [DllImport("user32.dll")]
        private static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
        [DllImport("user32.dll")]
        public static extern bool IsWindow(IntPtr hwnd);
        [DllImport("user32.dll")]
        private static extern bool IsWindowVisible(IntPtr hwnd);
        [DllImport("user32.dll")]
        private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("shcore.dll")]
        private static extern int SetProcessDpiAwareness(int value);
        [DllImport("user32.dll")]
        private static extern bool SetProcessDPIAware();
        [DllImport("kernel32.dll")]
        private static extern uint GetCurrentThreadId();
        [DllImport("user32.dll")]
        private static extern IntPtr GetThreadDesktop(uint threadId);
        [DllImport("user32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
        private static extern bool GetUserObjectInformation(IntPtr handle, int index,
            StringBuilder information, int length, out int requiredLength);

        public static string GetDesktopName() {
            IntPtr desktop = GetThreadDesktop(GetCurrentThreadId());
            if (desktop == IntPtr.Zero) return null;
            StringBuilder name = new StringBuilder(257);
            int required;
            // UOI_NAME = 2. GetThreadDesktop returns a borrowed handle: do not close it.
            return GetUserObjectInformation(desktop, 2, name, name.Capacity * 2, out required)
                ? name.ToString() : null;
        }

        public static DpiState EnterDpiContext() {
            DpiState state = new DpiState();
            try {
                IntPtr previous = SetThreadDpiAwarenessContext(new IntPtr(-4));
                if (previous != IntPtr.Zero) {
                    state.previous_context = previous;
                    state.mode = "per_monitor_v2";
                    state.per_monitor_aware = true;
                    return state;
                }
                previous = SetThreadDpiAwarenessContext(new IntPtr(-3));
                if (previous != IntPtr.Zero) {
                    state.previous_context = previous;
                    state.mode = "per_monitor";
                    state.per_monitor_aware = true;
                    return state;
                }
            } catch (EntryPointNotFoundException) { }
            try {
                if (SetProcessDpiAwareness(2) == 0) {
                    state.mode = "per_monitor_process";
                    state.per_monitor_aware = true;
                    return state;
                }
            } catch (DllNotFoundException) { }
              catch (EntryPointNotFoundException) { }
            if (SetProcessDPIAware()) state.mode = "system_aware";
            return state;
        }

        public static void LeaveDpiContext(DpiState state) {
            if (state.previous_context != IntPtr.Zero)
                SetThreadDpiAwarenessContext(state.previous_context);
        }

        public static IntPtr ParseHwnd(string text) {
            ulong value;
            if (text.StartsWith("0x", StringComparison.OrdinalIgnoreCase))
                value = Convert.ToUInt64(text.Substring(2), 16);
            else value = Convert.ToUInt64(text, 10);
            if (value == 0 || (IntPtr.Size == 4 && value > UInt32.MaxValue))
                throw new ArgumentException("Invalid hwnd.");
            return IntPtr.Size == 8 ? new IntPtr(unchecked((long)value))
                                   : new IntPtr(unchecked((int)value));
        }

        public static string FormatHwnd(IntPtr handle) {
            return "0x" + (IntPtr.Size == 8
                ? unchecked((ulong)handle.ToInt64()).ToString("X")
                : unchecked((uint)handle.ToInt32()).ToString("X"));
        }

        public static WindowList ListWindows(int limit, int pidFilter, bool includeHidden) {
            WindowList result = new WindowList();
            bool stopped = false;
            EnumWindowsProc callback = delegate(IntPtr hwnd, IntPtr data) {
                bool visible = IsWindowVisible(hwnd);
                if (!visible && !includeHidden) return true;
                uint pid;
                GetWindowThreadProcessId(hwnd, out pid);
                if (pidFilter != 0 && pid != (uint)pidFilter) return true;
                RECT rect;
                if (!GetWindowRect(hwnd, out rect)) return true; // Window may close mid-snapshot.
                if (result.windows.Count >= limit) {
                    result.truncated = true;
                    stopped = true;
                    return false;
                }
                StringBuilder title = new StringBuilder(513);
                StringBuilder className = new StringBuilder(257);
                GetWindowText(hwnd, title, title.Capacity);
                GetClassName(hwnd, className, className.Capacity);
                string processName = null;
                try {
                    using (Process p = Process.GetProcessById((int)pid)) {
                        processName = p.ProcessName;
                    }
                } catch (ArgumentException) { }
                  catch (InvalidOperationException) { }
                  catch (Win32Exception) { }
                result.windows.Add(new Window {
                    hwnd = FormatHwnd(hwnd),
                    title = title.ToString(), class_name = className.ToString(),
                    process_name = processName, pid = pid, is_visible = visible,
                    rect = new Rect { x=rect.Left, y=rect.Top,
                        width=rect.Right-rect.Left, height=rect.Bottom-rect.Top }
                });
                return true;
            };
            if (!EnumWindows(callback, IntPtr.Zero) && !stopped)
                throw new Win32Exception(Marshal.GetLastWin32Error());
            return result;
        }
    }
}
'@
}

function Get-Processes($Parameters) {
    $limit = Get-Integer $Parameters 'limit' 500 1 1000
    $targetPid = Get-Integer $Parameters 'pid' 0 0 ([int]::MaxValue)
    $includePath = Get-Boolean $Parameters 'include_path' $false
    $name = Get-Field $Parameters 'name' ''
    if (($name -isnot [string]) -or (Get-CodePointLength $name) -gt 256) {
        Stop-Bridge 'INVALID_REQUEST' 'name must be a string of at most 256 characters.'
    }
    $items = New-Object 'System.Collections.Generic.List[object]'
    $all = @()
    if ($targetPid -ne 0) {
        try { $all = @([Diagnostics.Process]::GetProcessById($targetPid)) }
        catch [ArgumentException] { } # A missing or exited PID is an empty result.
    } else {
        $all = [Diagnostics.Process]::GetProcesses()
    }
    $truncated = $false
    try {
        $ordered = $all
        if ($targetPid -eq 0) { $ordered = $all | Sort-Object Id }
        foreach ($process in $ordered) {
            try {
                $processName = $process.ProcessName
                if ($name -and ($processName.IndexOf($name, [StringComparison]::OrdinalIgnoreCase) -lt 0)) { continue }
                if ($items.Count -ge $limit) { $truncated = $true; break }
                $path = $null
                $pathStatus = 'not_requested'
                if ($includePath) {
                    try {
                        $path = Limit-Text $process.MainModule.FileName 2048
                        $pathStatus = 'available'
                    } catch { $pathStatus = 'unavailable' }
                }
                $processSessionId = $null
                $startTimeUtc = $null
                try { $processSessionId = $process.SessionId } catch { }
                try { $startTimeUtc = $process.StartTime.ToUniversalTime().ToString('o') } catch { }
                $identityStatus = 'available'
                if (($null -eq $processSessionId) -or ($null -eq $startTimeUtc)) { $identityStatus = 'partial' }
                $items.Add([ordered]@{
                    pid = $process.Id
                    name = Limit-Text $processName
                    session_id = $processSessionId
                    start_time_utc = $startTimeUtc
                    identity_status = $identityStatus
                    path = $path
                    path_status = $pathStatus
                })
            } catch { continue } # Processes can exit or deny access during enumeration.
        }
    } finally {
        foreach ($process in $all) { $process.Dispose() }
    }
    return [ordered]@{ processes = @($items.ToArray()); truncated = $truncated; limit = $limit }
}

function Get-Windows($Parameters) {
    $limit = Get-Integer $Parameters 'limit' 500 1 1000
    $targetPid = Get-Integer $Parameters 'pid' 0 0 ([int]::MaxValue)
    $includeHidden = Get-Boolean $Parameters 'include_hidden' $false
    Initialize-Native
    $dpi = [Starbridge.Native]::EnterDpiContext()
    try {
        $result = [Starbridge.Native]::ListWindows($limit, $targetPid, $includeHidden)
        $coordinates = 'guest-screen-dpi-context-dependent-pixels'
        if ($dpi.per_monitor_aware) { $coordinates = 'guest-screen-physical-pixels' }
        return [ordered]@{
            windows = @($result.windows.ToArray())
            truncated = $result.truncated
            limit = $limit
            dpi_awareness = $dpi.mode
            coordinate_space = $coordinates
            rect_source = 'GetWindowRect'
        }
    } finally { [Starbridge.Native]::LeaveDpiContext($dpi) }
}

function Get-UiOptions($Parameters) {
    $hwnd = Get-Field $Parameters 'hwnd'
    if (($hwnd -isnot [string]) -or ($hwnd -notmatch '^(0[xX][0-9a-fA-F]{1,16}|[0-9]{1,20})$')) {
        Stop-Bridge 'INVALID_REQUEST' 'hwnd must be a nonzero decimal or hexadecimal string.'
    }
    return [ordered]@{
        hwnd = $hwnd
        max_depth = Get-Integer $Parameters 'max_depth' 6 0 12
        max_nodes = Get-Integer $Parameters 'max_nodes' 300 1 1000
        timeout_ms = Get-Integer $Parameters 'timeout_ms' 10000 1000 20000
    }
}

function Add-UiWarning([string]$Warning) {
    if (($script:UiWarnings.Count -lt 16) -and !$script:UiWarnings.Contains($Warning)) {
        $script:UiWarnings.Add($Warning)
    }
}

function Visit-UiNode($Element, $ParentId, [int]$Depth) {
    if ($script:UiNodes.Count -ge $script:UiOptions.max_nodes) {
        $script:UiNodeLimitReached = $true
        return
    }
    $nodeId = 'n{0:D6}' -f ($script:UiNodes.Count + 1)
    try {
        $info = $Element.Cached
        $bounds = $info.BoundingRectangle
        $rect = $null
        $finiteBounds = !$bounds.IsEmpty
        foreach ($coordinate in @($bounds.X, $bounds.Y, $bounds.Width, $bounds.Height)) {
            if ([Double]::IsInfinity($coordinate) -or [Double]::IsNaN($coordinate)) { $finiteBounds = $false }
        }
        if ($finiteBounds) {
            $rect = [ordered]@{ x=$bounds.X; y=$bounds.Y; width=$bounds.Width; height=$bounds.Height }
        }
        $runtimeId = $null
        try { $runtimeId = @($Element.GetRuntimeId()) } catch { Add-UiWarning 'runtime_id_unavailable' }
        $script:UiNodes.Add([ordered]@{
            node_id = $nodeId
            parent_id = $ParentId
            depth = $Depth
            runtime_id = $runtimeId
            name = Limit-Text $info.Name
            control_type = $info.ControlType.ProgrammaticName.Replace('ControlType.', '')
            automation_id = Limit-Text $info.AutomationId
            class_name = Limit-Text $info.ClassName 256
            framework_id = Limit-Text $info.FrameworkId 128
            pid = $info.ProcessId
            rect = $rect
            is_enabled = $info.IsEnabled
            is_offscreen = $info.IsOffscreen
            is_password = $info.IsPassword
        })
    } catch {
        if ($Depth -eq 0) { Stop-Bridge 'UIA_UNAVAILABLE' 'The window is unavailable or its UI Automation provider cannot be read from this session.' }
        Add-UiWarning 'element_unavailable_or_access_denied'
        return
    }
    if ($Depth -ge $script:UiOptions.max_depth) {
        $script:UiDepthLimitReached = $true
        return
    }
    try {
        if ($script:UiNodes.Count -lt $script:UiOptions.max_nodes) {
            $child = $script:UiWalker.GetFirstChild($Element, $script:UiCache)
        } else {
            # Probe only for truncation; do not cache a node we cannot return.
            $child = $script:UiWalker.GetFirstChild($Element)
        }
    } catch {
        Add-UiWarning 'children_unavailable_or_access_denied'
        return
    }
    while ($null -ne $child) {
        if ($script:UiNodes.Count -ge $script:UiOptions.max_nodes) {
            $script:UiNodeLimitReached = $true
            return
        }
        Visit-UiNode $child $nodeId ($Depth + 1)
        try {
            if ($script:UiNodes.Count -lt $script:UiOptions.max_nodes) {
                $child = $script:UiWalker.GetNextSibling($child, $script:UiCache)
            } else {
                $child = $script:UiWalker.GetNextSibling($child)
            }
        } catch {
            Add-UiWarning 'siblings_unavailable_or_access_denied'
            return
        }
    }
}

function Get-UiTreeInWorker($Options) {
    Initialize-Native
    try { $windowHandle = [Starbridge.Native]::ParseHwnd($Options.hwnd) } catch {
        Stop-Bridge 'INVALID_REQUEST' 'hwnd is outside the range supported by this Windows process.'
    }
    if (![Starbridge.Native]::IsWindow($windowHandle)) {
        Stop-Bridge 'WINDOW_NOT_FOUND' 'No window with this hwnd is available in the current session.'
    }
    Add-Type -AssemblyName UIAutomationClient
    Add-Type -AssemblyName UIAutomationTypes
    Add-Type -AssemblyName WindowsBase
    $script:UiOptions = $Options
    $script:UiNodes = New-Object 'System.Collections.Generic.List[object]'
    $script:UiWarnings = New-Object 'System.Collections.Generic.List[string]'
    $script:UiNodeLimitReached = $false
    $script:UiDepthLimitReached = $false
    $script:UiWalker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
    $script:UiCache = New-Object System.Windows.Automation.CacheRequest
    foreach ($property in @(
        [System.Windows.Automation.AutomationElement]::NameProperty,
        [System.Windows.Automation.AutomationElement]::ControlTypeProperty,
        [System.Windows.Automation.AutomationElement]::AutomationIdProperty,
        [System.Windows.Automation.AutomationElement]::ClassNameProperty,
        [System.Windows.Automation.AutomationElement]::FrameworkIdProperty,
        [System.Windows.Automation.AutomationElement]::ProcessIdProperty,
        [System.Windows.Automation.AutomationElement]::BoundingRectangleProperty,
        [System.Windows.Automation.AutomationElement]::IsEnabledProperty,
        [System.Windows.Automation.AutomationElement]::IsOffscreenProperty,
        [System.Windows.Automation.AutomationElement]::IsPasswordProperty
    )) { $script:UiCache.Add($property) }
    $dpi = [Starbridge.Native]::EnterDpiContext()
    try {
        try {
            $root = [System.Windows.Automation.AutomationElement]::FromHandle($windowHandle)
            $root = $root.GetUpdatedCache($script:UiCache)
        } catch {
            Stop-Bridge 'UIA_UNAVAILABLE' 'UI Automation could not open this window in the current session.'
        }
        Visit-UiNode $root $null 0
        return [ordered]@{
            hwnd = [Starbridge.Native]::FormatHwnd($windowHandle)
            snapshot_id = [Guid]::NewGuid().ToString('N')
            view = 'control'
            nodes = @($script:UiNodes.ToArray())
            node_limit_reached = $script:UiNodeLimitReached
            depth_limit_reached = $script:UiDepthLimitReached
            max_nodes = $Options.max_nodes
            max_depth = $Options.max_depth
            warnings = @($script:UiWarnings.ToArray())
            coordinate_space = 'guest-screen-physical-pixels'
            dpi_awareness = $dpi.mode
        }
    } finally { [Starbridge.Native]::LeaveDpiContext($dpi) }
}

function Invoke-UiWorker($Options) {
    # A malicious or broken UIA provider can block inside a COM call. A timer in
    # the tree walk cannot interrupt that call, so bound a dedicated process.
    $powerShellPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) {
        $powerShellPath = Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
    }
    if (!(Test-Path -LiteralPath $powerShellPath -PathType Leaf)) {
        Stop-Bridge 'UIA_WORKER_UNAVAILABLE' 'Windows PowerShell 5.1 is required for the isolated UI Automation worker.'
    }
    $escapedScriptPath = $PSCommandPath.Replace("'", "''")
    $command = "& '$escapedScriptPath' -RequestBase64 '$RequestBase64' -UiWorker"
    $encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $powerShellPath
    $startInfo.Arguments = '-NoLogo -NoProfile -NonInteractive -Mta -ExecutionPolicy Bypass -EncodedCommand ' + $encodedCommand
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.StandardOutputEncoding = New-Object Text.UTF8Encoding($false)
    $startInfo.StandardErrorEncoding = New-Object Text.UTF8Encoding($false)
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $startInfo
    try {
        if (!$process.Start()) { Stop-Bridge 'UIA_WORKER_FAILED' 'The UI Automation worker could not start.' }
        $outputTask = $process.StandardOutput.ReadToEndAsync()
        $errorTask = $process.StandardError.ReadToEndAsync()
        if (!$process.WaitForExit($Options.timeout_ms)) {
            try { $process.Kill(); $null = $process.WaitForExit(2000) } catch { }
            Stop-Bridge 'UIA_TIMEOUT' 'The UI Automation worker exceeded timeout_ms. Worker termination was attempted; retry with a smaller tree.'
        }
        $output = $outputTask.GetAwaiter().GetResult()
        $null = $errorTask.GetAwaiter().GetResult() # Drain, never echo provider exception text.
        try { $response = ConvertFrom-Json -InputObject $output -ErrorAction Stop } catch {
            Stop-Bridge 'UIA_WORKER_FAILED' 'The UI Automation worker returned an invalid response.'
        }
        if ((Get-Field $response 'ok') -isnot [bool]) {
            Stop-Bridge 'UIA_WORKER_FAILED' 'The UI Automation worker returned an invalid response envelope.'
        }
        return $response
    } finally { $process.Dispose() }
}

try {
    if ($RequestBase64.Length -gt 24000) { Stop-Bridge 'INVALID_REQUEST' 'The encoded request is too large.' }
    try {
        $requestBytes = [Convert]::FromBase64String($RequestBase64)
        if ($requestBytes.Length -gt 16000) { throw 'Request too large.' }
        $strictUtf8 = New-Object Text.UTF8Encoding($false, $true)
        $requestText = $strictUtf8.GetString($requestBytes)
        # Windows PowerShell unwraps a top-level singleton JSON array. Reject
        # non-object syntax before parsing so [{...}] cannot become a request.
        if (!$requestText.TrimStart().StartsWith('{')) { throw 'The request must be a JSON object.' }
        $request = ConvertFrom-Json -InputObject $requestText -ErrorAction Stop
    } catch { Stop-Bridge 'INVALID_REQUEST' 'RequestBase64 must contain valid UTF-8 JSON of at most 16000 bytes.' }
    if (!(Test-JsonObject $request)) { Stop-Bridge 'INVALID_REQUEST' 'The request must be a JSON object.' }
    $action = Get-Field $request 'action'
    if (($action -isnot [string]) -or ($action -notin @('processes.list', 'windows.list', 'ui.tree'))) {
        Stop-Bridge 'UNKNOWN_ACTION' 'Supported actions: processes.list, windows.list, ui.tree.'
    }
    $parameters = Get-Field $request 'params' ([PSCustomObject]@{})
    if (!(Test-JsonObject $parameters)) { Stop-Bridge 'INVALID_REQUEST' 'params must be a JSON object.' }
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        Stop-Bridge 'UNSUPPORTED_PLATFORM' 'This guest helper requires Windows.'
    }
    if (($action -in @('windows.list', 'ui.tree')) -and
        (([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) -or ![Environment]::UserInteractive)) {
        Stop-Bridge 'INTERACTIVE_SESSION_REQUIRED' 'Desktop inspection requires the logged-in user interactive session; this helper is running in session 0 or a noninteractive context.'
    }
    if ($UiWorker -and ($action -ne 'ui.tree')) { Stop-Bridge 'INVALID_REQUEST' 'UiWorker accepts only ui.tree.' }
    switch ($action) {
        'processes.list' { $data = Get-Processes $parameters }
        'windows.list' { $data = Get-Windows $parameters }
        'ui.tree' {
            $options = Get-UiOptions $parameters
            if ($UiWorker) { $data = Get-UiTreeInWorker $options }
            else {
                $response = Invoke-UiWorker $options
                Write-Response $response
                if (!$response.ok) { exit 1 }
                exit 0
            }
        }
    }
    Write-Response ([ordered]@{ ok=$true; data=$data; meta=(New-Metadata $action $data) })
    exit 0
} catch {
    Write-Response ([ordered]@{
        ok = $false
        error = [ordered]@{ code=$script:FailureCode; message=$script:FailureMessage }
    })
    exit 1
}
