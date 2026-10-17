# Named development tasks. Profiles and state belong to the current Windows user.
$script:DevTaskModuleRoot = $PSScriptRoot
function Get-TaskRoot {
    $root = $env:STARBRIDGE_DEV_HOME
    if ([string]::IsNullOrWhiteSpace($root)) { $root = Join-Path $env:LOCALAPPDATA 'Starbridge\Developer' }
    if ([string]::IsNullOrWhiteSpace($root) -or ![IO.Path]::IsPathRooted($root)) { Stop-Dev 'TASK_CONFIG_INVALID' 'A local absolute developer state directory is required.' }
    return [IO.Path]::GetFullPath($root)
}
function Get-TaskField($Object,[string]$Name,$Default=$null) {
    if ($null -eq $Object) { return ,$Default }
    if ($Object -is [Collections.IDictionary]) { if ($Object.Contains($Name)) { return ,$Object[$Name] }; return ,$Default }
    $field=$Object.PSObject.Properties[$Name]
    if ($null -eq $field) { return ,$Default }
    return ,$field.Value
}
function Write-TaskJson([string]$Path,$Value) {
    $temporary=$Path+'.'+[Guid]::NewGuid().ToString('N')+'.tmp'
    try {
        $json=ConvertTo-Json -InputObject $Value -Depth 12 -Compress
        [IO.File]::WriteAllText($temporary,$json,(New-Object Text.UTF8Encoding($false)))
        if ([IO.File]::Exists($Path)) { [IO.File]::Replace($temporary,$Path,$null) } else { [IO.File]::Move($temporary,$Path) }
    } finally { if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) } }
}
function Read-TaskJson([string]$Path) {
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { Stop-Dev 'TASK_NOT_FOUND' 'No task state is available for this task identifier.' }
    $info=Get-Item -LiteralPath $Path
    if ($info.Length -gt 131072) { Stop-Dev 'TASK_STATE_INVALID' 'Task state exceeds its size budget.' }
    $file=$null;$reader=$null
    try {
        # State is atomically replaced by the worker. Readers permit replacement
        # while retaining their original snapshot handle (Windows FILE_SHARE_DELETE).
        $sharing=[IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete
        $file=New-Object IO.FileStream($Path,[IO.FileMode]::Open,[IO.FileAccess]::Read,$sharing)
        if ($file.Length -gt 131072) { Stop-Dev 'TASK_STATE_INVALID' 'Task state exceeds its size budget.' }
        $reader=New-Object IO.StreamReader($file,(New-Object Text.UTF8Encoding($false,$true)))
        return (ConvertFrom-Json -InputObject ($reader.ReadToEnd()) -ErrorAction Stop)
    } catch { Stop-Dev 'TASK_STATE_INVALID' 'Task state could not be read.' }
    finally {if ($null -ne $reader) {$reader.Dispose()};if ($null -ne $file) {$file.Dispose()}}
}
function Get-TaskDirectory([string]$TaskId) {
    if ($TaskId -cnotmatch '^[0-9a-f]{32}$') { Stop-Dev 'INVALID_REQUEST' 'Invalid task identifier.' }
    $directory=Join-Path (Join-Path (Get-TaskRoot) 'tasks') $TaskId
    if (!(Test-Path -LiteralPath $directory -PathType Container)) { Stop-Dev 'TASK_NOT_FOUND' 'The task identifier does not exist for this user.' }
    if (((Get-Item -LiteralPath $directory).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { Stop-Dev 'TASK_STATE_INVALID' 'Task state cannot be a reparse point.' }
    return $directory
}
function Test-TaskTerminal([string]$Status) { return $Status -in @('exited','cancelled','timed_out','failed','orphaned') }
function Get-TaskWorkerIdentity($Identity) {
    $process=$null
    try {
        $process=[Diagnostics.Process]::GetProcessById([int]$Identity.pid)
        if($process.HasExited){return @{valid=$false;reason='exited'}}
        if($process.StartTime.ToUniversalTime().Ticks -ne [long]$Identity.start_ticks){return @{valid=$false;reason='start_time_mismatch';ticks_delta=($process.StartTime.ToUniversalTime().Ticks-[long]$Identity.start_ticks)}}
        if(![string]::Equals([IO.Path]::GetFullPath($process.MainModule.FileName),[IO.Path]::GetFullPath($Identity.executable),[StringComparison]::OrdinalIgnoreCase)){return @{valid=$false;reason='executable_mismatch'}}
        return @{valid=$true;reason='matched'}
    } catch {
        $exception=$_.Exception
        for($depth=0;$depth -lt 8 -and $null -ne $exception.InnerException;$depth++){$exception=$exception.InnerException}
        $result=@{valid=$false;reason='unavailable';exception_type=$exception.GetType().Name}
        if($exception -is [ComponentModel.Win32Exception]){$result.native_error=[int]$exception.NativeErrorCode}
        return $result
    } finally {if($null-ne$process){$process.Dispose()}}
}
function Test-TaskWorker($Identity) { $observation=Get-TaskWorkerIdentity $Identity;return [bool]$observation.valid }

function Get-TaskView([string]$Directory) {
    $receipt=Read-TaskJson (Join-Path $Directory 'receipt.json')
    $statePath=Join-Path $Directory 'state.json'
    $state=$receipt
    if ([IO.File]::Exists($statePath)) { $state=Read-TaskJson $statePath }
    $status=[string]$state.status
    $workerIdentity=Get-TaskWorkerIdentity $receipt.worker
    if (!(Test-TaskTerminal $status) -and !$workerIdentity.valid) { $status='orphaned' }
    $stdout=Join-Path $Directory 'stdout.bin';$stderr=Join-Path $Directory 'stderr.bin'
    $stdoutBytes=0L;$stderrBytes=0L
    if ([IO.File]::Exists($stdout)) { $stdoutBytes=(Get-Item -LiteralPath $stdout).Length }
    if ([IO.File]::Exists($stderr)) { $stderrBytes=(Get-Item -LiteralPath $stderr).Length }
    $view=[ordered]@{ task_id=$receipt.task_id; profile=$receipt.profile; status=$status; created_at=$receipt.created_at
        pid=(Get-TaskField $state 'pid');start_time_utc=(Get-TaskField $state 'start_time_utc');exit_code=(Get-TaskField $state 'exit_code')
        stdout_bytes=$stdoutBytes;stderr_bytes=$stderrBytes
        stdout_truncated=[bool](Get-TaskField $state 'stdout_truncated' $false);stderr_truncated=[bool](Get-TaskField $state 'stderr_truncated' $false) }
    $view['worker_identity_reason']=[string]$workerIdentity.reason
    foreach($key in @('exception_type','native_error','ticks_delta')){
        if($workerIdentity.ContainsKey($key)){$view['worker_identity_'+$key]=$workerIdentity[$key]}
    }
    foreach ($entry in @(@('error_code','^[A-Z][A-Z0-9_]{0,63}$'),@('failure_stage','^[a-z_]{1,64}$'),@('failure_exception_type','^[A-Za-z0-9_.]{1,128}$'),@('failure_native_stage','^[a-z_]{1,64}$'))) {
        $value=Get-TaskField $state $entry[0]
        if($value -is [string] -and $value -cmatch $entry[1]){$view[$entry[0]]=$value}
    }
    foreach($field in @('failure_line','failure_native_error')){
        $value=Get-TaskField $state $field
        if(($value -is [int] -or $value -is [long]) -and $value-ge-2147483648 -and $value-le2147483647){$view[$field]=[long]$value}
    }
    return $view
}
function Assert-TaskAbsolutePath([string]$Path) {
    if ($Path -notmatch '^(?:[A-Za-z]:[\\/]|\\\\[^\\/]+[\\/][^\\/]+)' -or $Path.IndexOf([char]0) -ge 0) {
        Stop-Dev 'TASK_CONFIG_INVALID' 'Profiles require absolute executable and working-directory paths.'
    }
}
function Get-TaskStringArray($Value,[int]$MaxCount=64) {
    if ($Value -isnot [array] -or $Value.Count -gt $MaxCount) { Stop-Dev 'INVALID_REQUEST' 'Task arguments must be a bounded string array.' }
    $items=New-Object 'Collections.Generic.List[string]'
    foreach ($item in $Value) {
        if ($item -isnot [string] -or $item.Length -gt 2048 -or $item.IndexOf([char]0) -ge 0) { Stop-Dev 'INVALID_REQUEST' 'Task arguments must contain bounded strings without NUL.' }
        $items.Add($item)
    }
    return ,$items.ToArray()
}
function Invoke-TaskStart($p) {
    Assert-DevFields $p @('profile','args','env','timeout_ms')
    $name=Get-DevString $p 'profile' '' 64 -Required
    if ($name -cnotmatch '^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$') { Stop-Dev 'INVALID_REQUEST' 'Invalid task profile name.' }
    $root=Get-TaskRoot
    if (!(Test-Path -LiteralPath (Join-Path $root 'profiles.json') -PathType Leaf)) { Stop-Dev 'TASK_CONFIG_NOT_FOUND' 'Configure local named task profiles before starting a task.' }
    $config=Read-TaskJson (Join-Path $root 'profiles.json')
    Assert-DevFields $config @('version','profiles')
    $null=Get-DevInteger $config 'version' 1 1 1
    $profiles=Get-TaskField $config 'profiles'
    $profile=Get-TaskField $profiles $name
    if ($null -eq $profile) { Stop-Dev 'TASK_PROFILE_NOT_FOUND' 'The named task profile is not configured for this user.' }
    Assert-DevFields $profile @('executable','cwd','args','env','allowed_env','allow_extra_args','timeout_ms','max_output_bytes','interactive','show_window')
    $executable=Get-DevString $profile 'executable' '' 2048 -Required
    $cwd=Get-DevString $profile 'cwd' '' 2048 -Required
    Assert-TaskAbsolutePath $executable;Assert-TaskAbsolutePath $cwd
    if (!(Test-Path -LiteralPath $executable -PathType Leaf) -or !(Test-Path -LiteralPath $cwd -PathType Container)) { Stop-Dev 'TASK_CONFIG_INVALID' 'The configured executable or working directory is unavailable.' }
    $arguments=Get-TaskStringArray (Get-TaskField $profile 'args' @())
    $extra=Get-TaskStringArray (Get-TaskField $p 'args' @())
    if ($extra.Count -gt 0 -and !(Get-DevBoolean $profile 'allow_extra_args' $false)) { Stop-Dev 'TASK_POLICY_DENIED' 'This profile does not allow additional arguments.' }
    $arguments=@($arguments)+@($extra)
    $environment=New-Object 'Collections.Generic.Dictionary[string,string]' ([StringComparer]::OrdinalIgnoreCase)
    foreach ($source in @((Get-TaskField $profile 'env' ([PSCustomObject]@{})),(Get-TaskField $p 'env' ([PSCustomObject]@{})))) {
        if ($null -eq $source -or $source -is [array] -or $source -isnot [PSCustomObject]) { Stop-Dev 'INVALID_REQUEST' 'Task environment must be a string mapping.' }
        foreach ($property in $source.PSObject.Properties) {
            if ($property.Name -cnotmatch '^[A-Za-z_][A-Za-z0-9_]{0,63}$' -or $property.Value -isnot [string] -or $property.Value.Length -gt 4096 -or $property.Value.IndexOf([char]0) -ge 0) { Stop-Dev 'INVALID_REQUEST' 'Invalid task environment name or value.' }
        }
    }
    $fixedEnvironment=Get-TaskField $profile 'env' ([PSCustomObject]@{})
    foreach ($property in $fixedEnvironment.PSObject.Properties) { $environment[$property.Name]=[string]$property.Value }
    $allowed=Get-TaskStringArray (Get-TaskField $profile 'allowed_env' @()) 32
    $overrides=Get-TaskField $p 'env' ([PSCustomObject]@{})
    foreach ($property in $overrides.PSObject.Properties) {
        if ($property.Name -notin $allowed) { Stop-Dev 'TASK_POLICY_DENIED' 'This profile does not allow the requested environment override.' }
        $environment[$property.Name]=[string]$property.Value
    }
    $profileTimeout=Get-DevInteger $profile 'timeout_ms' 300000 1000 3600000
    $timeout=Get-DevInteger $p 'timeout_ms' $profileTimeout 1000 3600000
    if ($timeout -gt $profileTimeout) { Stop-Dev 'TASK_POLICY_DENIED' 'The requested timeout exceeds the configured task budget.' }
    $cap=Get-DevInteger $profile 'max_output_bytes' 1048576 1024 16777216
    $interactive=Get-DevBoolean $profile 'interactive' $false
    if ($interactive -and (([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) -or ![Environment]::UserInteractive)) { Stop-Dev 'INTERACTIVE_SESSION_REQUIRED' 'This task profile requires an interactive user desktop.' }
    $id=[Guid]::NewGuid().ToString('N');$directory=Join-Path (Join-Path $root 'tasks') $id
    $null=New-Item -ItemType Directory -Path $directory -Force
    $workerPath=Join-Path $script:DevTaskModuleRoot 'workers\task-worker.ps1'
    $powerShell=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) { $powerShell=Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe' }
    $request=[ordered]@{task_id=$id;profile=$name;executable=[IO.Path]::GetFullPath($executable);cwd=[IO.Path]::GetFullPath($cwd);args=@($arguments);env=$environment;timeout_ms=$timeout;max_output_bytes=$cap;show_window=(Get-DevBoolean $profile 'show_window' $interactive)}
    Write-TaskJson (Join-Path $directory 'request.json') $request
    $command="& '"+$workerPath.Replace("'","''")+"' -TaskDirectory '"+$directory.Replace("'","''")+"'"
    $encoded=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $start=New-Object Diagnostics.ProcessStartInfo
    $start.FileName=$powerShell;$start.Arguments='-NoLogo -NoProfile -NonInteractive -Mta -ExecutionPolicy Bypass -EncodedCommand '+$encoded
    $start.UseShellExecute=$false;$start.CreateNoWindow=$true
    $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    # Separate redirected pipes keep the detached worker from holding the
    # Parallels transport open. The worker emits no normal stdout/stderr.
    $worker=New-Object Diagnostics.Process;$worker.StartInfo=$start
    try {
        if (!$worker.Start()) { Stop-Dev 'TASK_START_FAILED' 'The task worker could not start.' }
        $null=$worker.StandardOutput.ReadToEndAsync();$null=$worker.StandardError.ReadToEndAsync()
        $identity=[ordered]@{pid=$worker.Id;start_ticks=$worker.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture);executable=$worker.MainModule.FileName}
        Write-TaskJson (Join-Path $directory 'receipt.json') ([ordered]@{task_id=$id;profile=$name;status='starting';created_at=[DateTime]::UtcNow.ToString('o');worker=$identity})
    } catch { Stop-Dev 'TASK_START_FAILED' 'The task worker could not be started or recorded.' }
    finally { $worker.Dispose() }
    return (Get-TaskView $directory)
}
function Invoke-TaskStatus($p) {
    Assert-DevFields $p @('task_id')
    $id=Get-DevString $p 'task_id' '' 32 -Required
    return (Get-TaskView (Get-TaskDirectory $id))
}
function Invoke-TaskOutput($p) {
    Assert-DevFields $p @('task_id','stream','offset','limit')
    $id=Get-DevString $p 'task_id' '' 32 -Required;$directory=Get-TaskDirectory $id
    $stream=Get-DevString $p 'stream' 'stdout' 6
    if ($stream -cnotin @('stdout','stderr')) { Stop-Dev 'INVALID_REQUEST' 'stream must be stdout or stderr.' }
    $offset=Get-DevInteger $p 'offset' 0 0 16777216;$limit=Get-DevInteger $p 'limit' 16384 1 65536
    $view=Get-TaskView $directory;$path=Join-Path $directory ($stream+'.bin');$buffer=New-Object byte[] $limit;$count=0;$length=0L
    if ([IO.File]::Exists($path)) {
        $file=New-Object IO.FileStream($path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::ReadWrite)
        try {$length=$file.Length;if ($offset -gt $length) {Stop-Dev 'INVALID_REQUEST' 'offset is beyond the currently stored output.'};$null=$file.Seek($offset,[IO.SeekOrigin]::Begin);$count=$file.Read($buffer,0,$limit)} finally {$file.Dispose()}
    } elseif ($offset -ne 0) {Stop-Dev 'INVALID_REQUEST' 'offset is beyond the currently stored output.'}
    return [ordered]@{task_id=$id;status=$view.status;stream=$stream;offset=$offset;next_offset=($offset+$count);data_base64=[Convert]::ToBase64String($buffer,0,$count);eof=((Test-TaskTerminal $view.status) -and ($offset+$count -ge $length));truncated=[bool]$view[$stream+'_truncated']}
}
function Invoke-TaskControl($p,[string]$Action) {
    Assert-DevFields $p @('task_id')
    $id=Get-DevString $p 'task_id' '' 32 -Required;$directory=Get-TaskDirectory $id;$view=Get-TaskView $directory
    if (Test-TaskTerminal $view.status) {$view['accepted']=$false;$view['control_status']='already_finished';return $view}
    $receipt=Read-TaskJson (Join-Path $directory 'receipt.json')
    if (!(Test-TaskWorker $receipt.worker)) {Stop-Dev 'TASK_IDENTITY_MISMATCH' 'The original task worker is no longer available.'}
    $controlId=[Guid]::NewGuid().ToString('N');$requestPath=Join-Path $directory ('control-'+$controlId+'.json');$answerPath=Join-Path $directory ('answer-'+$controlId+'.json')
    Write-TaskJson $requestPath ([ordered]@{action=$Action})
    $timer=[Diagnostics.Stopwatch]::StartNew()
    while ($timer.ElapsedMilliseconds -lt 3000 -and ![IO.File]::Exists($answerPath)) {Start-Sleep -Milliseconds 50}
    $view=Get-TaskView $directory
    if ([IO.File]::Exists($answerPath)) {
        $answer=Read-TaskJson $answerPath;$view['accepted']=[bool]$answer.accepted;$view['control_status']=[string]$answer.control_status
        if ($null -ne $answer.PSObject.Properties['reason']) {$view['reason']=[string]$answer.reason}
        [IO.File]::Delete($answerPath)
    } elseif (Test-TaskTerminal $view.status) {
        $view['accepted']=$false;$view['control_status']='already_finished'
        if ([IO.File]::Exists($requestPath)) {[IO.File]::Delete($requestPath)}
    } else {$view['accepted']=$false;$view['control_status']='pending'}
    return $view
}
if ($null -ne (Get-Command Register-DeveloperAction -ErrorAction SilentlyContinue)) {
    Register-DeveloperAction -Name 'task.start' -Handler {param($p) Invoke-TaskStart $p} -Mutation:$true -Interactive:$false
    Register-DeveloperAction -Name 'task.status' -Handler {param($p) Invoke-TaskStatus $p} -Mutation:$false -Interactive:$false
    Register-DeveloperAction -Name 'task.output' -Handler {param($p) Invoke-TaskOutput $p} -Mutation:$false -Interactive:$false
    Register-DeveloperAction -Name 'task.cancel' -Handler {param($p) Invoke-TaskControl $p 'cancel'} -Mutation:$true -Interactive:$false
    Register-DeveloperAction -Name 'task.close' -Handler {param($p) Invoke-TaskControl $p 'close'} -Mutation:$true -Interactive:$true
}
