# Internal UIA functions shared by isolated developer workers. No entry point,
# registry mutation, input injection, or external application launch occurs here.
function Stop-DevUi([string]$Code, [string]$Message) {
    $script:DevUiErrorCode = $Code
    $script:DevUiErrorMessage = $Message
    throw $Message
}

function Get-DevUiField($Object, [string]$Name, $Default = $null) {
    if ($Object -is [Collections.IDictionary]) {
        if ($Object.Contains($Name)) { return ,$Object[$Name] }
    } elseif ($null -ne $Object) {
        $property = $Object.PSObject.Properties[$Name]
        if ($null -ne $property) { return ,$property.Value }
    }
    return ,$Default
}

function Get-DevUiCodePointLength([string]$Text) {
    $count = 0
    for ($i = 0; $i -lt $Text.Length; $i++) {
        if ([char]::IsSurrogatePair($Text, $i)) { $i++ }
        $count++
    }
    return $count
}

function Limit-DevUiText($Value, [int]$Max) {
    if ($null -eq $Value) { return $null }
    $text = [string]$Value
    $result = New-Object Text.StringBuilder
    $offset = 0
    for ($count = 0; ($offset -lt $text.Length) -and ($count -lt $Max); $count++) {
        if ([char]::IsSurrogatePair($text, $offset)) {
            $null = $result.Append($text, $offset, 2)
            $offset += 2
        } else {
            # A provider may return malformed UTF-16. Preserve the code-point
            # budget while ensuring the JSON value can be encoded as UTF-8.
            if ([char]::IsSurrogate($text[$offset])) { $null = $result.Append([char]0xFFFD) }
            else { $null = $result.Append($text[$offset]) }
            $offset++
        }
    }
    return $result.ToString()
}

function Assert-DevUiObject($Object) {
    if (($null -eq $Object) -or (($Object -isnot [Collections.IDictionary]) -and
        ($Object.GetType() -ne [Management.Automation.PSCustomObject]))) {
        Stop-DevUi 'INVALID_ARGUMENT' 'UI parameters and selectors must be JSON objects.'
    }
}

function Assert-DevUiFields($Object, [string[]]$Allowed) {
    Assert-DevUiObject $Object
    $keys = @()
    if ($Object -is [Collections.IDictionary]) { $keys = @($Object.Keys) }
    else { $keys = @($Object.PSObject.Properties | ForEach-Object { $_.Name }) }
    foreach ($key in $keys) {
        if ($key -cnotin $Allowed) { Stop-DevUi 'INVALID_ARGUMENT' 'An unsupported UI query field was supplied.' }
    }
}

function Get-DevUiInteger($Object, [string]$Name, [int]$Default, [int]$Min, [int]$Max) {
    $value = Get-DevUiField $Object $Name $Default
    if ((($value -isnot [int]) -and ($value -isnot [long])) -or ($value -lt $Min) -or ($value -gt $Max)) {
        Stop-DevUi 'INVALID_ARGUMENT' "$Name must be a canonical integer within its documented range."
    }
    return [int]$value
}

function Get-DevUiString($Object, [string]$Name, [string]$Default, [int]$Max) {
    $value = Get-DevUiField $Object $Name $Default
    if (($value -isnot [string]) -or (Get-DevUiCodePointLength $value) -gt $Max -or $value.IndexOf([char]0) -ge 0) {
        Stop-DevUi 'INVALID_ARGUMENT' "$Name must be a bounded string without NUL."
    }
    return $value
}

function Resolve-DevUiSelector($Object) {
    Assert-DevUiFields $Object @('name', 'name_contains', 'automation_id', 'control_type', 'class_name')
    $result = [ordered]@{}
    foreach ($key in @('name', 'name_contains', 'automation_id', 'control_type', 'class_name')) {
        $value = Get-DevUiField $Object $key
        $exists = $false
        if ($Object -is [Collections.IDictionary]) { $exists = $Object.Contains($key) }
        else { $exists = $null -ne $Object.PSObject.Properties[$key] }
        if (!$exists) { continue }
        $max = 256
        if ($key -eq 'automation_id') { $max = 512 }
        if ($key -eq 'control_type') { $max = 64 }
        $result[$key] = Get-DevUiString $Object $key '' $max
    }
    return ,$result
}

function ConvertTo-DevUiTimestamp([DateTimeOffset]$Instant) {
    return $Instant.UtcDateTime.ToString('yyyy-MM-ddTHH:mm:ss.fffffff', [Globalization.CultureInfo]::InvariantCulture).TrimEnd('0').TrimEnd('.') + 'Z'
}

function Resolve-DevUiQueryParameters($Object) {
    Assert-DevUiFields $Object @('hwnd', 'pid', 'start_time_utc', 'selector', 'ancestor', 'mode', 'offset', 'limit', 'max_depth', 'max_scan_nodes', 'timeout_ms', 'include_value')
    $hwnd = Get-DevUiString $Object 'hwnd' '' 128
    if ($hwnd -cnotmatch '^(0[xX][0-9a-fA-F]+|[0-9]+)$') { Stop-DevUi 'INVALID_ARGUMENT' 'hwnd must be an ASCII decimal or hexadecimal string.' }
    try {
        if ($hwnd.StartsWith('0x', [StringComparison]::OrdinalIgnoreCase)) { $number = [Convert]::ToUInt64($hwnd.Substring(2), 16) }
        else { $number = [Convert]::ToUInt64($hwnd, 10) }
        if (($number -eq 0) -or ($number -gt [long]::MaxValue)) { throw 'Range.' }
    } catch { Stop-DevUi 'INVALID_ARGUMENT' 'hwnd must be positive and fit the signed 63-bit range.' }
    $pidValue = Get-DevUiInteger $Object 'pid' 0 1 ([int]::MaxValue)
    if ($pidValue -eq 0) { Stop-DevUi 'INVALID_ARGUMENT' 'pid is required.' }
    $start = Get-DevUiString $Object 'start_time_utc' '' 40
    $instant = [DateTimeOffset]::MinValue
    if (($start -cnotmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,7})?Z$') -or
        ![DateTimeOffset]::TryParse($start, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal, [ref]$instant)) {
        Stop-DevUi 'INVALID_ARGUMENT' 'start_time_utc must be a UTC RFC3339 process start time with at most seven fractional digits.'
    }
    $mode = Get-DevUiString $Object 'mode' 'list' 8
    if ($mode -cnotin @('list', 'one')) { Stop-DevUi 'INVALID_ARGUMENT' 'mode must be list or one.' }
    $defaultLimit = 50
    if ($mode -ceq 'one') { $defaultLimit = 1 }
    $result = [ordered]@{
        hwnd = '0x' + $number.ToString('x')
        pid = $pidValue
        start_time_utc = ConvertTo-DevUiTimestamp $instant
        selector = Resolve-DevUiSelector (Get-DevUiField $Object 'selector' ([PSCustomObject]@{}))
        ancestor = Resolve-DevUiSelector (Get-DevUiField $Object 'ancestor' ([PSCustomObject]@{}))
        mode = $mode
        offset = Get-DevUiInteger $Object 'offset' 0 0 4999
        limit = Get-DevUiInteger $Object 'limit' $defaultLimit 1 200
        max_depth = Get-DevUiInteger $Object 'max_depth' 6 0 12
        max_scan_nodes = Get-DevUiInteger $Object 'max_scan_nodes' 2000 1 5000
        timeout_ms = Get-DevUiInteger $Object 'timeout_ms' 10000 1000 20000
        include_value = Get-DevUiField $Object 'include_value' $false
    }
    if ($result.include_value -isnot [bool]) { Stop-DevUi 'INVALID_ARGUMENT' 'include_value must be a boolean.' }
    if (($mode -ceq 'one') -and (($result.offset -ne 0) -or ($result.limit -ne 1))) {
        Stop-DevUi 'INVALID_ARGUMENT' 'mode one requires offset 0 and limit 1.'
    }
    return ,$result
}

function Test-DevUiSelector($Info, $Selector) {
    foreach ($key in $Selector.Keys) {
        $actual = ''
        switch -CaseSensitive ($key) {
            'name' { $actual = [string]$Info.Name }
            'name_contains' { $actual = [string]$Info.Name }
            'automation_id' { $actual = [string]$Info.AutomationId }
            'control_type' { $actual = $Info.ControlType.ProgrammaticName.Replace('ControlType.', '') }
            'class_name' { $actual = [string]$Info.ClassName }
        }
        if ($key -ceq 'name_contains') {
            if ($actual.IndexOf([string]$Selector[$key], [StringComparison]::OrdinalIgnoreCase) -lt 0) { return $false }
        } elseif (![string]::Equals($actual, [string]$Selector[$key], [StringComparison]::Ordinal)) { return $false }
    }
    return $true
}

function Initialize-DevUiNative {
    if ('StarbridgeDeveloperUi.Native' -as [type]) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace StarbridgeDeveloperUi {
    public static class Native {
        [DllImport("user32.dll")] public static extern bool IsWindow(IntPtr hwnd);
        [DllImport("user32.dll")] public static extern IntPtr GetAncestor(IntPtr hwnd, uint flags);
        [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
        [DllImport("user32.dll")] private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        public static uint WindowPid(IntPtr hwnd) { uint pid; GetWindowThreadProcessId(hwnd, out pid); return pid; }
        public static IntPtr Handle(long number) {
            return IntPtr.Size == 8 ? new IntPtr(number) : new IntPtr(unchecked((int)number));
        }
        public static IntPtr EnterDpi() {
            try {
                IntPtr previous = SetThreadDpiAwarenessContext(new IntPtr(-4));
                return previous != IntPtr.Zero ? previous : SetThreadDpiAwarenessContext(new IntPtr(-3));
            } catch (EntryPointNotFoundException) { return IntPtr.Zero; }
        }
        public static void LeaveDpi(IntPtr previous) {
            if (previous != IntPtr.Zero) SetThreadDpiAwarenessContext(previous);
        }
    }
}
'@
}

function Assert-DevUiWindow($Parameters) {
    $number = [Convert]::ToInt64($Parameters.hwnd.Substring(2), 16)
    if (([IntPtr]::Size -eq 4) -and ($number -gt [uint32]::MaxValue)) { Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The window handle does not fit this worker.' }
    $handle = [StarbridgeDeveloperUi.Native]::Handle($number)
    if (![StarbridgeDeveloperUi.Native]::IsWindow($handle)) { Stop-DevUi 'WINDOW_NOT_FOUND' 'The requested window is no longer available.' }
    if ([StarbridgeDeveloperUi.Native]::GetAncestor($handle, 2) -ne $handle) { Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The requested handle must identify a top-level window.' }
    if ([StarbridgeDeveloperUi.Native]::WindowPid($handle) -ne $Parameters.pid) { Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The window belongs to a different process.' }
    $process = $null
    try {
        $process = [Diagnostics.Process]::GetProcessById($Parameters.pid)
        $actualStart = ConvertTo-DevUiTimestamp ([DateTimeOffset]$process.StartTime.ToUniversalTime())
        if ($actualStart -cne $Parameters.start_time_utc) { Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The process identity changed; refresh the process and window observations.' }
    } catch {
        if ($script:DevUiErrorCode -ceq 'WINDOW_IDENTITY_MISMATCH') { throw }
        Stop-DevUi 'WINDOW_IDENTITY_UNAVAILABLE' 'The process start time is unavailable in this session.'
    } finally { if ($null -ne $process) { $process.Dispose() } }
    return $handle
}

function Add-DevUiWarning([string]$Code, [bool]$Incomplete = $false) {
    if (($script:DevUiWarnings.Count -lt 16) -and !$script:DevUiWarnings.Contains($Code)) { $script:DevUiWarnings.Add($Code) }
    if ($Incomplete) { $script:DevUiComplete = $false }
}

function Get-DevUiRect($Bounds) {
    if ($Bounds.IsEmpty) { return $null }
    foreach ($number in @($Bounds.X, $Bounds.Y, $Bounds.Width, $Bounds.Height)) {
        if ([double]::IsNaN($number) -or [double]::IsInfinity($number)) { return $null }
    }
    return [ordered]@{x=$Bounds.X;y=$Bounds.Y;width=$Bounds.Width;height=$Bounds.Height}
}

function Get-DevUiItem($Element, $Info, [string]$NodeId, $ParentId, [int]$Depth) {
    $password = $Info.IsPassword
    try { $password = $password -or $Element.Current.IsPassword }
    catch { $password = $true; Add-DevUiWarning 'password_state_unavailable' }
    $runtime = $null
    try { $runtime = @($Element.GetRuntimeId()) } catch { Add-DevUiWarning 'runtime_id_unavailable' }
    $patterns = New-Object 'Collections.Generic.List[string]'
    try {
        foreach ($pattern in $Element.GetSupportedPatterns()) {
            if ($patterns.Count -ge 32) { break }
            $name = $pattern.ProgrammaticName -replace 'PatternIdentifiers\.Pattern$', ''
            if (($name.Length -le 64) -and !$patterns.Contains($name)) { $patterns.Add($name) }
        }
    } catch { Add-DevUiWarning 'patterns_unavailable' }
    $state = [ordered]@{toggle=$null;is_selected=$null;expand_collapse=$null;value_read_only=$null;range_value=$null;range_minimum=$null;range_maximum=$null}
    $value = $null
    $valueTruncated = $false
    $valueStatus = 'not_requested'
    if ($password) { $valueStatus = 'password_skipped' }
    else {
        foreach ($entry in @(
            @{pattern=[System.Windows.Automation.TogglePattern]::Pattern;kind='toggle'},
            @{pattern=[System.Windows.Automation.SelectionItemPattern]::Pattern;kind='selection'},
            @{pattern=[System.Windows.Automation.ExpandCollapsePattern]::Pattern;kind='expand'},
            @{pattern=[System.Windows.Automation.RangeValuePattern]::Pattern;kind='range'},
            @{pattern=[System.Windows.Automation.ValuePattern]::Pattern;kind='value'}
        )) {
            $currentPattern = $null
            try {
                if (!$Element.TryGetCurrentPattern($entry.pattern, [ref]$currentPattern)) { continue }
                switch ($entry.kind) {
                    'toggle' { $state.toggle = $currentPattern.Current.ToggleState.ToString() }
                    'selection' { $state.is_selected = $currentPattern.Current.IsSelected }
                    'expand' { $state.expand_collapse = $currentPattern.Current.ExpandCollapseState.ToString() }
                    'range' {
                        foreach ($property in @(@{key='range_value';name='Value'},@{key='range_minimum';name='Minimum'},@{key='range_maximum';name='Maximum'})) {
                            $number = $currentPattern.Current.($property.name)
                            if (![double]::IsNaN($number) -and ![double]::IsInfinity($number)) { $state[$property.key] = $number }
                        }
                    }
                    'value' {
                        $state.value_read_only = $currentPattern.Current.IsReadOnly
                        if ($script:DevUiOptions.include_value) {
                            $raw = [string]$currentPattern.Current.Value
                            $value = Limit-DevUiText $raw 2048
                            $valueTruncated = (Get-DevUiCodePointLength $raw) -gt 2048
                            $valueStatus = 'available'
                        }
                    }
                }
            } catch {
                Add-DevUiWarning 'pattern_state_unavailable'
                if (($entry.kind -eq 'value') -and $script:DevUiOptions.include_value) { $valueStatus = 'unavailable' }
            }
        }
        if ($script:DevUiOptions.include_value -and ($valueStatus -eq 'not_requested')) {
            $textPattern = $null
            try {
                if ($Element.TryGetCurrentPattern([System.Windows.Automation.TextPattern]::Pattern, [ref]$textPattern)) {
                    # GetText bounds UTF-16 string units. Read enough for 2049
                    # supplementary code points so the 2048-point cap and its
                    # truncation flag remain correct even for all-emoji text.
                    $raw = [string]$textPattern.DocumentRange.GetText(4098)
                    $value = Limit-DevUiText $raw 2048
                    $valueTruncated = (Get-DevUiCodePointLength $raw) -gt 2048
                    $valueStatus = 'available'
                } else { $valueStatus = 'unsupported' }
            } catch { $valueStatus = 'unavailable'; Add-DevUiWarning 'value_unavailable' }
        }
    }
    return [ordered]@{
        node_id=$NodeId;parent_id=$ParentId;depth=$Depth;runtime_id=$runtime
        pid=$Info.ProcessId;name=(Limit-DevUiText $Info.Name 512);control_type=$Info.ControlType.ProgrammaticName.Replace('ControlType.','')
        automation_id=(Limit-DevUiText $Info.AutomationId 512);class_name=(Limit-DevUiText $Info.ClassName 256);framework_id=(Limit-DevUiText $Info.FrameworkId 128)
        rect=(Get-DevUiRect $Info.BoundingRectangle);is_enabled=$Info.IsEnabled;is_offscreen=$Info.IsOffscreen;is_password=$password
        patterns=@($patterns.ToArray());state=$state;value=$value;value_status=$valueStatus;value_truncated=$valueTruncated
    }
}

function Visit-DevUiQueryNode($Element, $ParentId, [int]$Depth, [object[]]$Ancestors) {
    if ($script:DevUiScanned -ge $script:DevUiOptions.max_scan_nodes) {
        $script:DevUiNodeLimit = $true; $script:DevUiComplete = $false; return
    }
    $script:DevUiScanned++
    $nodeId = 'n{0:D6}' -f $script:DevUiScanned
    try { $info = $Element.Cached } catch { Add-DevUiWarning 'element_unavailable' $true; return }
    $ancestorMatch = $script:DevUiOptions.ancestor.Count -eq 0
    foreach ($ancestorInfo in $Ancestors) {
        if (Test-DevUiSelector $ancestorInfo $script:DevUiOptions.ancestor) { $ancestorMatch = $true; break }
    }
    if ($ancestorMatch -and (Test-DevUiSelector $info $script:DevUiOptions.selector)) {
        $script:DevUiMatched++
        if ($script:DevUiMatched -eq 1) { $script:DevUiFirstMatch = $Element }
        if (($script:DevUiMatched -gt $script:DevUiOptions.offset) -and ($script:DevUiItems.Count -lt $script:DevUiOptions.limit)) {
            $script:DevUiItems.Add((Get-DevUiItem $Element $info $nodeId $ParentId $Depth))
        }
    }
    if ($Depth -ge $script:DevUiOptions.max_depth) { $script:DevUiDepthLimit = $true; return }
    try {
        if ($script:DevUiScanned -lt $script:DevUiOptions.max_scan_nodes) { $child = $script:DevUiWalker.GetFirstChild($Element, $script:DevUiCache) }
        else { $child = $script:DevUiWalker.GetFirstChild($Element) }
    } catch { Add-DevUiWarning 'children_unavailable' $true; return }
    while ($null -ne $child) {
        if ($script:DevUiScanned -ge $script:DevUiOptions.max_scan_nodes) {
            $script:DevUiNodeLimit = $true; $script:DevUiComplete = $false; return
        }
        Visit-DevUiQueryNode $child $nodeId ($Depth + 1) (@($Ancestors) + @($info))
        try {
            if ($script:DevUiScanned -lt $script:DevUiOptions.max_scan_nodes) { $child = $script:DevUiWalker.GetNextSibling($child, $script:DevUiCache) }
            else { $child = $script:DevUiWalker.GetNextSibling($child) }
        } catch { Add-DevUiWarning 'siblings_unavailable' $true; return }
    }
}

function Invoke-DevUiQueryCore($Parameters) {
    if (([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) -or
        ([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) -or ![Environment]::UserInteractive) {
        Stop-DevUi 'INTERACTIVE_SESSION_REQUIRED' 'UI queries require the logged-in Windows desktop session.'
    }
    Initialize-DevUiNative
    $handle = Assert-DevUiWindow $Parameters
    Add-Type -AssemblyName UIAutomationClient
    Add-Type -AssemblyName UIAutomationTypes
    Add-Type -AssemblyName WindowsBase
    $script:DevUiOptions = $Parameters
    $script:DevUiFirstMatch = $null
    $script:DevUiScanned = 0; $script:DevUiMatched = 0
    $script:DevUiComplete = $true; $script:DevUiNodeLimit = $false; $script:DevUiDepthLimit = $false
    $script:DevUiItems = New-Object 'Collections.Generic.List[object]'
    $script:DevUiWarnings = New-Object 'Collections.Generic.List[string]'
    $script:DevUiWalker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
    $script:DevUiCache = New-Object System.Windows.Automation.CacheRequest
    foreach ($name in @('Name','ControlType','AutomationId','ClassName','FrameworkId','ProcessId','BoundingRectangle','IsEnabled','IsOffscreen','IsPassword')) {
        $script:DevUiCache.Add([System.Windows.Automation.AutomationElement]::($name + 'Property'))
    }
    $previousDpi = [StarbridgeDeveloperUi.Native]::EnterDpi()
    try {
        $root = [System.Windows.Automation.AutomationElement]::FromHandle($handle)
        $root = $root.GetUpdatedCache($script:DevUiCache)
        $rootRuntime = @($root.GetRuntimeId())
        Visit-DevUiQueryNode $root $null 0 @()
        $null = Assert-DevUiWindow $Parameters
        $currentRoot = [System.Windows.Automation.AutomationElement]::FromHandle($handle)
        if (($rootRuntime -join ',') -cne (@($currentRoot.GetRuntimeId()) -join ',')) {
            Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The window was replaced during inspection; refresh its identity.'
        }
        if ($Parameters.mode -ceq 'one') {
            if (!$script:DevUiComplete) { Stop-DevUi 'QUERY_INCOMPLETE' 'The query scope could not be searched completely; narrow it or increase its scan budget.' }
            if ($script:DevUiMatched -eq 0) { Stop-DevUi 'ELEMENT_NOT_FOUND' 'No element matched the selector in the requested scope.' }
            if ($script:DevUiMatched -gt 1) { Stop-DevUi 'AMBIGUOUS_MATCH' 'More than one element matched; add selector or ancestor conditions.' }
        }
        return [ordered]@{
            window=[ordered]@{hwnd=$Parameters.hwnd;pid=$Parameters.pid;start_time_utc=$Parameters.start_time_utc}
            snapshot_id=[Guid]::NewGuid().ToString('N');timestamp=[DateTime]::UtcNow.ToString('o');coordinate_space='guest-screen-physical-pixels';pagination='live'
            mode=$Parameters.mode;offset=$Parameters.offset;limit=$Parameters.limit;max_depth=$Parameters.max_depth;max_scan_nodes=$Parameters.max_scan_nodes;include_value=$Parameters.include_value
            scanned_nodes=$script:DevUiScanned;matched_count=$script:DevUiMatched;returned_count=$script:DevUiItems.Count
            items=@($script:DevUiItems.ToArray());has_more=($script:DevUiMatched -gt ($Parameters.offset + $script:DevUiItems.Count))
            complete=$script:DevUiComplete;node_limit_reached=$script:DevUiNodeLimit;depth_limit_reached=$script:DevUiDepthLimit;warnings=@($script:DevUiWarnings.ToArray())
        }
    } finally { [StarbridgeDeveloperUi.Native]::LeaveDpi($previousDpi) }
}
