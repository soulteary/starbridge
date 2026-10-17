function Invoke-DevUiActionWorker([string]$Name, $Parameters) {
    $timeout = Get-DevInteger $Parameters 'timeout_ms' 10000 1000 20000
    Invoke-DevUiWorker 'ui-action-worker.ps1' ([ordered]@{action=$Name;params=$Parameters}) $timeout $true
}

Register-DeveloperAction -Name 'ui.act' -Description 'Perform one supported UIA operation after a fresh unique query.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'ui.act' $p }
Register-DeveloperAction -Name 'window.focus' -Description 'Request ordinary foreground activation of an identified window.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'window.focus' $p }
Register-DeveloperAction -Name 'window.resize' -Description 'Resize an identified window using physical guest coordinates.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'window.resize' $p }
Register-DeveloperAction -Name 'input.key' -Description 'Send one bounded named-key chord to an identified foreground window.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'input.key' $p }
Register-DeveloperAction -Name 'input.mouse' -Description 'Send one mouse operation after foreground, client-bound and hit-test checks.' -Mutation:$true -Interactive:$true -Handler { param($p) Invoke-DevUiActionWorker 'input.mouse' $p }
