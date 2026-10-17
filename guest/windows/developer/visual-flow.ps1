# UIA is a separate bounded worker: a hung provider cannot discard the image or
# overwrite the visual worker's mutation/error state.
function Invoke-DevVisualAction([string]$Name,$Parameters) {
    $timeout=Get-DevInteger $Parameters 'timeout_ms' 15000 2000 20000
    $mutation=$Name -ceq 'ui.click'
    $timer=[Diagnostics.Stopwatch]::StartNew()
    $data=Invoke-DevUiWorker 'visual-worker.ps1' ([ordered]@{action=$Name;params=$Parameters}) $timeout $mutation 6291456
    if(Get-DevBoolean $Parameters 'include_uia' $true) {
        $observation=$data.observation
        $remaining=$timeout-[int]$timer.ElapsedMilliseconds
        $queryBudget=[Math]::Min($remaining,(Get-DevInteger $Parameters 'uia_timeout_ms' 1500 1000 5000))
        $query=[ordered]@{hwnd=$observation.hwnd;pid=$observation.pid;start_time_utc=$observation.start_time_utc;limit=64;max_depth=6;max_scan_nodes=500;include_value=$false;timeout_ms=$queryBudget}
        try {
            if($queryBudget -lt 1000){Stop-Dev 'UIA_TIMEOUT' 'The observation deadline left no UIA budget.'}
            $uia=Invoke-DevUiWorker 'ui-query-worker.ps1' $query $query.timeout_ms
            $data.uia=[PSCustomObject]@{status='available';complete=[bool]$uia.complete;data=$uia}
            $data.source='uia-and-visual'
        } catch {
            $code=$script:DevFailureCode
            if($code -cnotmatch '^[A-Z][A-Z0-9_]{0,63}$'){$code='UIA_UNAVAILABLE'}
            $data.uia=[PSCustomObject]@{status='unavailable';complete=$false;error_code=$code}
            # This optional read failed after a successful capture/dispatch. Do
            # not retain its error as the next operation's failure metadata.
            $script:DevFailureCode='INTERNAL_ERROR';$script:DevFailureMessage='The developer action could not complete.';$script:DevFailureDetails=$null
        }
    }
    return $data
}
Register-DeveloperAction -Name 'ui.observe' -Description 'Observe an identified window with a bounded image and optional UIA; issue a 30-second visual observation token.' -Mutation:$false -Interactive:$true -Handler {param($p) Invoke-DevVisualAction 'ui.observe' $p}
Register-DeveloperAction -Name 'ui.click' -Description 'Dispatch one guarded image-coordinate click, then return a new observation; business verification remains pending.' -Mutation:$true -Interactive:$true -Handler {param($p) Invoke-DevVisualAction 'ui.click' $p}
