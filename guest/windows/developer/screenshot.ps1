Register-DeveloperAction -Name 'screenshot.capture' -Description 'Request a bounded PNG rendering of an identified window without activating it.' -Mutation:$false -Interactive:$true -Handler {
    param($p)
    $timeout = Get-DevInteger $p 'timeout_ms' 10000 1000 20000
    Invoke-DevUiWorker 'screenshot-worker.ps1' $p $timeout $false 6291456
}
