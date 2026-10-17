function Resolve-DevUiActionParameters([string]$Name,$Object) {
    if($Name -cnotin @('ui.act','window.focus','window.resize','input.key','input.mouse')){Stop-DevUi 'INVALID_ARGUMENT' 'Unsupported UI action.'}
    $allowed=@('hwnd','pid','start_time_utc','timeout_ms','lock_timeout_ms')
    switch -CaseSensitive($Name){
        'ui.act'{$allowed+=@('operation','selector','ancestor','max_depth','max_scan_nodes','expected_runtime_id','value','horizontal','vertical')}
        'window.resize'{$allowed+=@('width','height','x','y')}
        'input.key'{$allowed+=@('key','modifiers')}
        'input.mouse'{$allowed+=@('event','button','x','y','wheel_delta')}
    }
    Assert-DevUiFields $Object $allowed
    $query=[PSCustomObject]@{
        hwnd=(Get-DevUiField $Object 'hwnd');pid=(Get-DevUiField $Object 'pid');start_time_utc=(Get-DevUiField $Object 'start_time_utc')
    }
    if($Name -ceq 'ui.act'){
        foreach($key in @('selector','ancestor','max_depth','max_scan_nodes')){
            $property=$Object.PSObject.Properties[$key]
            if($null -ne $property){$query|Add-Member NoteProperty $key $property.Value}
        }
        $query|Add-Member NoteProperty mode 'one'
    }
    $normalized=Resolve-DevUiQueryParameters $query
    $result=[ordered]@{hwnd=$normalized.hwnd;pid=$normalized.pid;start_time_utc=$normalized.start_time_utc}
    $result['timeout_ms']=Get-DevUiInteger $Object 'timeout_ms' 10000 1000 20000
    $result['lock_timeout_ms']=Get-DevUiInteger $Object 'lock_timeout_ms' 0 0 5000
    if($result.lock_timeout_ms -ge $result.timeout_ms){Stop-DevUi 'INVALID_ARGUMENT' 'lock_timeout_ms must be smaller than timeout_ms.'}
    switch -CaseSensitive($Name){
        'ui.act'{
            $operation=Get-DevUiString $Object 'operation' '' 32
            if($operation -cnotin @('invoke','set_value','toggle','select','expand','collapse','scroll','scroll_into_view','focus')){Stop-DevUi 'INVALID_ARGUMENT' 'Unsupported semantic UI operation.'}
            if($normalized.selector.Count -eq 0){Stop-DevUi 'INVALID_ARGUMENT' 'ui.act requires a nonempty selector.'}
            $result['operation']=$operation
            foreach($key in @('selector','ancestor','max_depth','max_scan_nodes')){$result[$key]=$normalized[$key]}
            $runtime=Get-DevUiField $Object 'expected_runtime_id'
            if($null -ne $Object.PSObject.Properties['expected_runtime_id']){
                if(($runtime -isnot [array]) -or $runtime.Count -lt 1 -or $runtime.Count -gt 64){Stop-DevUi 'INVALID_ARGUMENT' 'expected_runtime_id must be a bounded integer array.'}
                $values=New-Object 'Collections.Generic.List[object]'
                foreach($item in $runtime){$values.Add((Get-DevUiInteger ([PSCustomObject]@{item=$item}) 'item' 0 ([int]::MinValue) ([int]::MaxValue)))}
                $result['expected_runtime_id']=@($values.ToArray())
            }
            if($operation -ceq 'set_value'){
                if($null -eq $Object.PSObject.Properties['value']){Stop-DevUi 'INVALID_ARGUMENT' 'set_value requires value.'}
                $result['value']=Get-DevUiString $Object 'value' '' 4096
            }elseif($null -ne $Object.PSObject.Properties['value']){Stop-DevUi 'INVALID_ARGUMENT' 'value is only valid for set_value.'}
            foreach($key in @('horizontal','vertical')){
                if($operation -ceq 'scroll'){
                    $amount=Get-DevUiString $Object $key 'none' 32
                    if($amount -cnotin @('none','small_decrement','small_increment','large_decrement','large_increment')){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid scroll amount.'}
                    $result[$key]=$amount
                }elseif($null -ne $Object.PSObject.Properties[$key]){Stop-DevUi 'INVALID_ARGUMENT' 'Scroll amounts are only valid for scroll.'}
            }
            if($operation -ceq 'scroll' -and $result.horizontal -ceq 'none' -and $result.vertical -ceq 'none'){Stop-DevUi 'INVALID_ARGUMENT' 'scroll requires a nonzero direction.'}
        }
        'window.resize'{
            foreach($key in @('width','height')){
                $result[$key]=Get-DevUiInteger $Object $key 0 64 8192
                if($result[$key] -eq 0){Stop-DevUi 'INVALID_ARGUMENT' 'Resize dimensions are required.'}
            }
            foreach($key in @('x','y')){if($null -ne $Object.PSObject.Properties[$key]){$result[$key]=Get-DevUiInteger $Object $key 0 -65535 65535}}
        }
        'input.key'{
            $result['key']=Get-DevUiString $Object 'key' '' 16
            $null=ConvertTo-DevUiVirtualKey $result.key
            $modifiers=Get-DevUiField $Object 'modifiers' @()
            if(($modifiers -isnot [array]) -or $modifiers.Count -gt 3){Stop-DevUi 'INVALID_ARGUMENT' 'modifiers must be a bounded array.'}
            $seen=New-Object 'Collections.Generic.List[string]'
            foreach($modifier in $modifiers){
                if(($modifier -isnot [string]) -or ($modifier -cnotin @('Control','Alt','Shift')) -or $seen.Contains($modifier)){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid or repeated modifier.'}
                $seen.Add($modifier)
            }
            $result['modifiers']=@($seen.ToArray())
        }
        'input.mouse'{
            $result['event']=Get-DevUiString $Object 'event' 'click' 16
            if($result.event -cnotin @('click','move','wheel')){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid mouse event.'}
            foreach($key in @('x','y')){
                if($null -eq $Object.PSObject.Properties[$key]){Stop-DevUi 'INVALID_ARGUMENT' 'Mouse coordinates are required.'}
                $result[$key]=Get-DevUiInteger $Object $key 0 -65535 65535
            }
            if($result.event -ceq 'click'){
                $result['button']=Get-DevUiString $Object 'button' 'left' 16
                if($result.button -cnotin @('left','right','middle')){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid mouse button.'}
            }elseif($null -ne $Object.PSObject.Properties['button']){Stop-DevUi 'INVALID_ARGUMENT' 'button is only valid for click.'}
            if($result.event -ceq 'wheel'){
                $result['wheel_delta']=Get-DevUiInteger $Object 'wheel_delta' 0 -1200 1200
                if($result.wheel_delta -eq 0 -or $result.wheel_delta%120 -ne 0){Stop-DevUi 'INVALID_ARGUMENT' 'Invalid wheel_delta.'}
            }elseif($null -ne $Object.PSObject.Properties['wheel_delta']){Stop-DevUi 'INVALID_ARGUMENT' 'wheel_delta is only valid for wheel.'}
        }
    }
    return ,$result
}

function ConvertTo-DevUiVirtualKey([string]$Key) {
    if($Key -cmatch '^[A-Z0-9]$'){return [ushort][char]$Key[0]}
    $codes=@{Enter=13;Tab=9;Escape=27;Space=32;Backspace=8;Delete=46;Home=36;End=35;PageUp=33;PageDown=34;Left=37;Right=39;Up=38;Down=40;Insert=45}
    if($Key -cmatch '^F([1-9]|1[0-2])$'){return [ushort](111+[int]$Key.Substring(1))}
    foreach($name in $codes.Keys){if($Key -ceq $name){return [ushort]$codes[$name]}}
    Stop-DevUi 'INVALID_ARGUMENT' 'key must be a supported named keyboard key.'
}

function Get-DevUiSessionMutexName {
    return 'Local\StarbridgeDeveloperUiSession-' + [Diagnostics.Process]::GetCurrentProcess().SessionId
}

function Enter-DevUiSessionLease([int]$TimeoutMs) {
    $mutex=New-Object Threading.Mutex($false,(Get-DevUiSessionMutexName))
    $acquired=$false
    try{$acquired=$mutex.WaitOne($TimeoutMs)}
    catch [Threading.AbandonedMutexException]{
        $mutex.ReleaseMutex();$mutex.Dispose()
        Stop-DevUi 'SESSION_RECOVERY_REQUIRED' 'A previous UI worker ended while holding this session. Inspect application state before another write.'
    }catch{$mutex.Dispose();Stop-DevUi 'SESSION_LOCK_UNAVAILABLE' 'The interactive session lock is unavailable.'}
    if(!$acquired){$mutex.Dispose();Stop-DevUi 'SESSION_BUSY' 'Another UI worker owns this interactive session; no action was started.'}
    return $mutex
}

function Get-DevUiRequiredPattern($Element,$Pattern) {
    $value=$null
    try{$supported=$Element.TryGetCurrentPattern($Pattern,[ref]$value)}catch{Stop-DevUi 'PATTERN_UNAVAILABLE' 'The UI pattern is unavailable; no operation was dispatched.'}
    if(!$supported -or $null -eq $value){Stop-DevUi 'UNSUPPORTED_PATTERN' 'The selected control does not support this operation.'}
    return $value
}

function Invoke-DevUiSemanticOperation($Element,$Parameters) {
    try{$info=$Element.Current}catch{Stop-DevUi 'STALE_ELEMENT' 'The selected control is unavailable; query it again.'}
    if(!$info.IsEnabled){Stop-DevUi 'ELEMENT_DISABLED' 'The selected control is disabled.'}
    if($info.IsOffscreen -and $Parameters.operation -cne 'scroll_into_view'){Stop-DevUi 'ELEMENT_OFFSCREEN' 'The selected control is offscreen; scroll it into view first.'}
    $pattern=$null
    switch -CaseSensitive($Parameters.operation){
        'invoke'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.InvokePattern]::Pattern)}
        'set_value'{
            $pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.ValuePattern]::Pattern)
            if($pattern.Current.IsReadOnly){Stop-DevUi 'ELEMENT_READ_ONLY' 'The selected value is read-only.'}
        }
        'toggle'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.TogglePattern]::Pattern)}
        'select'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.SelectionItemPattern]::Pattern)}
        'expand'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.ExpandCollapsePattern]::Pattern)}
        'collapse'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.ExpandCollapsePattern]::Pattern)}
        'scroll'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.ScrollPattern]::Pattern)}
        'scroll_into_view'{$pattern=Get-DevUiRequiredPattern $Element ([System.Windows.Automation.ScrollItemPattern]::Pattern)}
    }
    $script:DevUiMutationBegan=$true
    try{
        switch -CaseSensitive($Parameters.operation){
            'invoke'{$null=$pattern.Invoke()}
            'set_value'{$null=$pattern.SetValue($Parameters.value)}
            'toggle'{$null=$pattern.Toggle()}
            'select'{$null=$pattern.Select()}
            'expand'{$null=$pattern.Expand()}
            'collapse'{$null=$pattern.Collapse()}
            'scroll'{
                $amounts=@{none='NoAmount';small_decrement='SmallDecrement';small_increment='SmallIncrement';large_decrement='LargeDecrement';large_increment='LargeIncrement'}
                $horizontal=[Enum]::Parse([System.Windows.Automation.ScrollAmount],$amounts[$Parameters.horizontal])
                $vertical=[Enum]::Parse([System.Windows.Automation.ScrollAmount],$amounts[$Parameters.vertical])
                $null=$pattern.Scroll($horizontal,$vertical)
            }
            'scroll_into_view'{$null=$pattern.ScrollIntoView()}
            'focus'{$null=$Element.SetFocus()}
            default{Stop-DevUi 'INVALID_ARGUMENT' 'Unsupported semantic UI operation.'}
        }
    }catch{Stop-DevUi 'UI_OPERATION_FAILED' 'The provider did not confirm the operation. Its outcome is unknown; inspect state before retrying.'}
}

function Assert-DevUiMutationContext($Parameters,[IntPtr]$Handle,[string]$Name) {
    $process=$null
    try{
        $process=[Diagnostics.Process]::GetProcessById($Parameters.pid)
        if($process.SessionId -ne [Diagnostics.Process]::GetCurrentProcess().SessionId){Stop-DevUi 'WINDOW_IDENTITY_MISMATCH' 'The target belongs to a different Windows session.'}
    }finally{if($null -ne $process){$process.Dispose()}}
    if(![StarbridgeDeveloperInput.Native]::InputDesktopAccessible($Handle)){Stop-DevUi 'INPUT_DESKTOP_UNAVAILABLE' 'The target and caller must share the accessible input desktop.'}
    if(![StarbridgeDeveloperInput.Native]::IsWindowVisible($Handle) -or ![StarbridgeDeveloperInput.Native]::IsWindowEnabled($Handle)){
        Stop-DevUi 'WINDOW_NOT_INTERACTABLE' 'The target window must be visible and enabled.'
    }
    if($Name -cne 'window.focus' -and [StarbridgeDeveloperInput.Native]::IsIconic($Handle)){Stop-DevUi 'WINDOW_MINIMIZED' 'Focus and restore the window before this operation.'}
}

function Assert-DevUiForeground([IntPtr]$Handle) {
    if([StarbridgeDeveloperInput.Native]::GetForegroundWindow() -ne $Handle){Stop-DevUi 'TARGET_NOT_FOREGROUND' 'The identified target is not the foreground window; focus it explicitly first.'}
    if(![StarbridgeDeveloperInput.Native]::PhysicalInputIdle()){Stop-DevUi 'INPUT_BUSY' 'A modifier or mouse button is already held; no input was sent.'}
}

function Invoke-DevUiActionCore([string]$Name,$Parameters) {
    if(([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) -or ([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) -or ![Environment]::UserInteractive){
        Stop-DevUi 'INTERACTIVE_SESSION_REQUIRED' 'UI actions require the logged-in Windows interactive session.'
    }
    Initialize-DevUiNative;Initialize-DevUiInputNative
    $lease=$null;$previousDpi=[IntPtr]::Zero
    try{
        $lease=Enter-DevUiSessionLease $Parameters.lock_timeout_ms
        $handle=Assert-DevUiWindow $Parameters
        Assert-DevUiMutationContext $Parameters $handle $Name
        $data=[ordered]@{
            action=$Name;window=[ordered]@{hwnd=$Parameters.hwnd;pid=$Parameters.pid;start_time_utc=$Parameters.start_time_utc}
            timestamp='';outcome='dispatched';coordination='windows-session-mutex'
        }
        switch -CaseSensitive($Name){
            'ui.act'{
                $query=[PSCustomObject]@{
                    hwnd=$Parameters.hwnd;pid=$Parameters.pid;start_time_utc=$Parameters.start_time_utc
                    selector=$Parameters.selector;ancestor=$Parameters.ancestor;mode='one';limit=1;offset=0
                    max_depth=$Parameters.max_depth;max_scan_nodes=$Parameters.max_scan_nodes;timeout_ms=$Parameters.timeout_ms;include_value=$false
                }
                $observation=Invoke-DevUiQueryCore (Resolve-DevUiQueryParameters $query)
                $element=$script:DevUiFirstMatch
                if($null -eq $element){Stop-DevUi 'ELEMENT_NOT_FOUND' 'The matched UI element is unavailable.'}
                $runtime=@($element.GetRuntimeId())
                if($runtime.Count -lt 1 -or $runtime.Count -gt 64 -or ($runtime -join ',') -cne ($observation.items[0].runtime_id -join ',')){Stop-DevUi 'STALE_ELEMENT' 'The matched UI element changed during inspection.'}
                if($Parameters.Contains('expected_runtime_id') -and ($runtime -join ',') -cne ($Parameters.expected_runtime_id -join ',')){Stop-DevUi 'STALE_ELEMENT' 'The expected runtime ID no longer matches; query the control again.'}
                $null=Assert-DevUiWindow $Parameters
                Assert-DevUiMutationContext $Parameters $handle $Name
                $info=$element.Current
                $data['operation']=$Parameters.operation
                $data['element']=[ordered]@{runtime_id=$runtime;control_type=$info.ControlType.ProgrammaticName.Replace('ControlType.','');is_password=$info.IsPassword}
                Invoke-DevUiSemanticOperation $element $Parameters
            }
            'window.focus'{
                if([StarbridgeDeveloperInput.Native]::GetForegroundWindow() -eq $handle -and ![StarbridgeDeveloperInput.Native]::IsIconic($handle)){$data['outcome']='already_satisfied'}
                else{
                    $script:DevUiMutationBegan=$true
                    if(![StarbridgeDeveloperInput.Native]::RequestFocus($handle)){Stop-DevUi 'FOREGROUND_DENIED' 'Windows did not grant foreground activation. Inspect the window; no input was sent.'}
                }
            }
            'window.resize'{
                $previousDpi=[StarbridgeDeveloperUi.Native]::EnterDpi()
                if($previousDpi -eq [IntPtr]::Zero){Stop-DevUi 'DPI_CONTEXT_UNAVAILABLE' 'Physical-coordinate resizing requires a per-monitor DPI context.'}
                $before=[StarbridgeDeveloperInput.Native]::WindowRect($handle)
                if($null -eq $before){Stop-DevUi 'WINDOW_NOT_FOUND' 'The window geometry is unavailable.'}
                $x=$before.x;$y=$before.y
                if($Parameters.Contains('x')){$x=$Parameters.x};if($Parameters.Contains('y')){$y=$Parameters.y}
                $script:DevUiMutationBegan=$true
                if(![StarbridgeDeveloperInput.Native]::Resize($handle,$x,$y,$Parameters.width,$Parameters.height)){Stop-DevUi 'WINDOW_RESIZE_FAILED' 'Windows did not confirm the resize. Inspect the actual geometry before retrying.'}
                $data['rect']=[StarbridgeDeveloperInput.Native]::WindowRect($handle)
                if($null -eq $data.rect){Stop-DevUi 'WINDOW_NOT_FOUND' 'The window disappeared after the resize request.'}
                $data['coordinate_space']='guest-screen-physical-pixels'
            }
            'input.key'{
                Assert-DevUiForeground $handle
                $keys=New-Object 'Collections.Generic.List[ushort]'
                $modifierKeys=@{Control=17;Alt=18;Shift=16}
                foreach($modifier in $Parameters.modifiers){$keys.Add([ushort]$modifierKeys[$modifier])}
                $keys.Add((ConvertTo-DevUiVirtualKey $Parameters.key))
                $null=Assert-DevUiWindow $Parameters;Assert-DevUiForeground $handle
                $script:DevUiMutationBegan=$true
                $data['input_count']=[StarbridgeDeveloperInput.Native]::KeyChord($keys.ToArray())
                if($data.input_count -lt 1){Stop-DevUi 'INPUT_FAILED' 'Input was partially inserted or denied; its outcome is unknown. No action retry was attempted.'}
                $data['foreground_after']='0x'+[StarbridgeDeveloperInput.Native]::GetForegroundWindow().ToInt64().ToString('x')
            }
            'input.mouse'{
                $previousDpi=[StarbridgeDeveloperUi.Native]::EnterDpi()
                if($previousDpi -eq [IntPtr]::Zero){Stop-DevUi 'DPI_CONTEXT_UNAVAILABLE' 'Mouse coordinates require a per-monitor DPI context.'}
                Assert-DevUiForeground $handle
                if(![StarbridgeDeveloperInput.Native]::ClientHit($handle,$Parameters.x,$Parameters.y)){Stop-DevUi 'TARGET_COVERED' 'The physical point is outside the target client area or belongs to a covering window.'}
                $button='left';$delta=0
                if($Parameters.Contains('button')){$button=$Parameters.button};if($Parameters.Contains('wheel_delta')){$delta=$Parameters.wheel_delta}
                $null=Assert-DevUiWindow $Parameters;Assert-DevUiForeground $handle
                $script:DevUiMutationBegan=$true
                $data['input_count']=[StarbridgeDeveloperInput.Native]::MouseOperation($Parameters.x,$Parameters.y,$Parameters.event,$button,$delta)
                if($data.input_count -lt 1){Stop-DevUi 'INPUT_FAILED' 'Mouse input was partially inserted or denied; its outcome is unknown. No click retry was attempted.'}
                $data['foreground_after']='0x'+[StarbridgeDeveloperInput.Native]::GetForegroundWindow().ToInt64().ToString('x')
            }
        }
        $data['timestamp']=[DateTime]::UtcNow.ToString('o')
        return ,$data
    }finally{
        [StarbridgeDeveloperUi.Native]::LeaveDpi($previousDpi)
        if($null -ne $lease){try{$lease.ReleaseMutex()}finally{$lease.Dispose()}}
    }
}
