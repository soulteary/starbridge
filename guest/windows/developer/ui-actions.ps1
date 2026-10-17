function Assert-DevUiTextWire($Parameters) {
    $property=$Parameters.PSObject.Properties['text']
    if($null -eq $property -or $property.Value -isnot [string]){Stop-Dev 'INVALID_ARGUMENT' 'text must be a nonempty valid Unicode string without NUL.' @{outcome='not_started'}}
    $value=$property.Value
    if($value.Length -eq 0 -or $value.Length -gt 4096 -or $value.IndexOf([char]0) -ge 0){Stop-Dev 'INVALID_ARGUMENT' 'text must be a bounded valid Unicode string without NUL.' @{outcome='not_started'}}
    # Validate before the wrapper's default UTF-8 encoder can replace an
    # unpaired surrogate. The isolated worker also enforces scalar budgets.
    try{$null=(New-Object Text.UTF8Encoding($false,$true)).GetByteCount($value)}
    catch{Stop-Dev 'INVALID_ARGUMENT' 'text must contain valid Unicode without unpaired surrogate units.' @{outcome='not_started'}}
}

function Invoke-DevUiActionWorker([string]$Name, $Parameters) {
    if($Name -ceq 'input.text'){Assert-DevUiTextWire $Parameters}
    $timeout = Get-DevInteger $Parameters 'timeout_ms' 10000 1000 20000
    Invoke-DevUiWorker 'ui-action-worker.ps1' ([ordered]@{action=$Name;params=$Parameters}) $timeout $true
}

Register-DeveloperAction -Name 'ui.act' -Description 'Perform one supported UIA operation after a fresh unique query.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'ui.act' $p }
Register-DeveloperAction -Name 'window.focus' -Description 'Request ordinary foreground activation of an identified window.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'window.focus' $p }
Register-DeveloperAction -Name 'window.resize' -Description 'Resize an identified window using physical guest coordinates.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'window.resize' $p }
Register-DeveloperAction -Name 'input.key' -Description 'Send one bounded named-key chord to an identified foreground window.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'input.key' $p }
Register-DeveloperAction -Name 'input.text' -Description 'Insert bounded literal Unicode text into an identified foreground window.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'input.text' $p }
Register-DeveloperAction -Name 'input.mouse' -Description 'Send one mouse operation after foreground, client-bound and hit-test checks.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'input.mouse' $p }
