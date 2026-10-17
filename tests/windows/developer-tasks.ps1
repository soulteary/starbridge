#requires -Version 5.1
<# Synthetic tasks only: creates its own executable, profiles and state below a
   fresh temporary directory. Does not call Parallels or inspect existing apps. #>
[CmdletBinding()]
param([string]$HelperPath=(Join-Path $PSScriptRoot '..\..\guest\windows\developer-bridge.ps1'))
Set-StrictMode -Version 2.0
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$previous=$env:STARBRIDGE_DEV_HOME
$root=Join-Path ([IO.Path]::GetTempPath()) ('starbridge-task-test-'+[Guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $root
$env:STARBRIDGE_DEV_HOME=$root
$helper=(Resolve-Path -LiteralPath $HelperPath).ProviderPath
$powerShell=Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess){$powerShell=Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'}
$ids=New-Object 'Collections.Generic.List[string]';$phase='setup';$restoreReceipts=@{}
$script:TaskAssertion='none';$script:TaskError='none';$script:TaskStatus='none'
$script:TaskWorkerStage='none';$script:TaskWorkerType='none';$script:TaskWorkerLine=0
$script:TaskNativeStage='none';$script:TaskNativeError=0;$script:TaskIdentity='none';$script:TaskIdentityType='none';$script:TaskIdentityNative=0;$script:TaskIdentityDelta=0
function Assert-TaskTest([bool]$Condition,[string]$Label){
 if(!$Condition){
  if($Label -match '^[A-Za-z0-9 /_.:-]{1,96}$'){$script:TaskAssertion=$Label}else{$script:TaskAssertion='invalid_assertion_label'}
  throw 'Synthetic task assertion failed.'
 }
}
function Get-TaskTestField($Object,[string]$Name,$Default=$null){
 if($null-eq$Object){return $Default};$property=$Object.PSObject.Properties[$Name]
 if($null-eq$property){return $Default};return $property.Value
}
function Remember-TaskTest($Response){
 $script:TaskError='none'
 if(!$Response.ok){
  $errorObject=Get-TaskTestField $Response 'error';$code=Get-TaskTestField $errorObject 'code' ''
  if($code -is [string] -and $code -cmatch '^[A-Z][A-Z0-9_]{0,63}$'){$script:TaskError=$code}
 }
 $data=Get-TaskTestField $Response 'data'
 $code=Get-TaskTestField $data 'error_code' ''
 if($code -is [string] -and $code -cmatch '^[A-Z][A-Z0-9_]{0,63}$'){$script:TaskError=$code}
 foreach($item in @(@('status','TaskStatus','^(starting|running|closing|cancelling|exited|cancelled|timed_out|failed|orphaned)$'),@('failure_stage','TaskWorkerStage','^[a-z_]{1,64}$'),@('failure_exception_type','TaskWorkerType','^[A-Za-z0-9_.]{1,128}$'),@('failure_native_stage','TaskNativeStage','^[a-z_]{1,64}$'),@('worker_identity_reason','TaskIdentity','^(matched|unavailable|exited|start_time_mismatch|executable_mismatch)$'),@('worker_identity_exception_type','TaskIdentityType','^[A-Za-z0-9_.]{1,128}$'))){
  $value=Get-TaskTestField $data $item[0] ''
  if($value -is [string] -and $value -cmatch $item[2]){Set-Variable -Name $item[1] -Scope Script -Value $value}
 }
 foreach($item in @(@('failure_line','TaskWorkerLine'),@('failure_native_error','TaskNativeError'),@('worker_identity_native_error','TaskIdentityNative'),@('worker_identity_ticks_delta','TaskIdentityDelta'))){
  $value=Get-TaskTestField $data $item[0]
  if(($value -is [int] -or $value -is [long]) -and $value -ge -2147483648 -and $value -le 2147483647){Set-Variable -Name $item[1] -Scope Script -Value $value}
 }
}
function Invoke-TaskTest([string]$Action,$Parameters,[switch]$Write){
 $request=ConvertTo-Json -InputObject @{action=$Action;params=$Parameters} -Depth 12 -Compress
 $encoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($request))
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$powerShell;$start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$helper+'" -RequestBase64 '+$encoded
 if($Write){$start.Arguments+=' -AllowWrite'}
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $start.StandardOutputEncoding=New-Object Text.UTF8Encoding($false,$true)
 $process=New-Object Diagnostics.Process;$process.StartInfo=$start
 try{
  $null=$process.Start();$stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
  if(!$process.WaitForExit(30000)){try{$process.Kill()}catch{};throw 'Test helper deadline exceeded.'}
  $text=$stdout.GetAwaiter().GetResult();$diagnostics=$stderr.GetAwaiter().GetResult()
  Assert-TaskTest ([string]::IsNullOrWhiteSpace($diagnostics)) 'Helper wrote unexpected diagnostics.'
  try{$response=ConvertFrom-Json -InputObject $text -ErrorAction Stop}catch{$script:TaskAssertion='helper_json_decode';throw 'Synthetic helper JSON invalid.'}
  Assert-TaskTest (($response.ok -is [bool]) -and (($process.ExitCode -eq 0) -eq $response.ok)) 'Envelope/exit mismatch.'
  Remember-TaskTest $response
  return $response
 }finally{$process.Dispose()}
}
function Start-TaskTest([string]$Profile,$More=([ordered]@{})){
 $params=@{profile=$Profile};foreach($key in $More.Keys){$params[$key]=$More[$key]}
 $response=Invoke-TaskTest 'task.start' $params -Write
 Assert-TaskTest ($response.ok) 'Synthetic task failed to start.'
 $ids.Add($response.data.task_id);return $response.data.task_id
}
function Wait-TaskTest([string]$Id){
 $timer=[Diagnostics.Stopwatch]::StartNew()
 while($timer.ElapsedMilliseconds -lt 20000){
  $response=Invoke-TaskTest 'task.status' @{task_id=$Id};Assert-TaskTest $response.ok 'Status query failed.'
  if($response.data.status -in @('exited','cancelled','timed_out','failed','orphaned')){return $response.data}
  Start-Sleep -Milliseconds 100
 }
 throw 'Synthetic task did not complete within its test deadline.'
}
try{
 $fixture=Join-Path $root 'fixture.exe'
 Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;
public class SyntheticTask {
 [STAThread] public static int Main(string[] args) {
  string mode=args[0],directory=args[1];
  if(mode=="echo") {
   List<string> encoded=new List<string>();for(int i=2;i<args.Length;i++)encoded.Add(Convert.ToBase64String(Encoding.UTF8.GetBytes(args[i])));
   File.WriteAllLines(Path.Combine(directory,"arguments.txt"),encoded.ToArray());
   File.WriteAllText(Path.Combine(directory,"environment.txt"),Environment.GetEnvironmentVariable("STARBRIDGE_TEST_LABEL")??"");
   byte[] output=Encoding.UTF8.GetBytes("hello \u4e2d\u6587");using(Stream stream=Console.OpenStandardOutput()){stream.Write(output,0,output.Length);}Console.Error.Write("stderr fixture");return 7;
  }
  if(mode=="flood") {Console.Write(new string('x',65536));Console.Error.Write(new string('y',65536));return 0;}
  if(mode=="tree") {
   Process.Start(new ProcessStartInfo(Process.GetCurrentProcess().MainModule.FileName,"child \""+directory+"\""){UseShellExecute=false,CreateNoWindow=true});
   Thread.Sleep(60000);return 0;
  }
  if(mode=="child") {File.WriteAllText(Path.Combine(directory,"child.txt"),Process.GetCurrentProcess().Id.ToString());Thread.Sleep(60000);return 0;}
  if(mode=="gui") {using(Form form=new Form()){form.Text="Starbridge synthetic task";Application.Run(form);}return 0;}
  Thread.Sleep(60000);return 0;
 }
}
'@ -OutputAssembly $fixture -OutputType ConsoleApplication -ReferencedAssemblies @('System.dll','System.Windows.Forms.dll','System.Drawing.dll')
 $profiles=[ordered]@{}
 foreach($name in @('echo','flood','tree','sleep','timeout','gui')){
  $mode=$name;if($name -ceq 'timeout'){$mode='tree'}
  $budget=60000;if($name -ceq 'timeout'){$budget=1500}
  $profiles[$name]=[ordered]@{executable=$fixture;cwd=$root;args=@($mode,$root);allowed_env=@('STARBRIDGE_TEST_LABEL');allow_extra_args=($name -ceq 'echo');timeout_ms=$budget;max_output_bytes=1024;interactive=($name -ceq 'gui');show_window=($name -ceq 'gui')}
 }
 # Individually valid configured arguments can still exceed the native limit.
 # Keep this profile in the guest config so transport request limits do not
 # hide the complete executable + fixed + extra argument budget.
 $longFixed=New-Object 'Collections.Generic.List[string]'
 for($index=0;$index -lt 15;$index++){$longFixed.Add(('x'*2048))}
 $longFixed.Add(('y'*1500))
 $profiles['command-limit']=[ordered]@{executable=$fixture;cwd=$root;args=@($longFixed.ToArray());allow_extra_args=$true;timeout_ms=60000;max_output_bytes=1024}
 [IO.File]::WriteAllText((Join-Path $root 'profiles.json'),(ConvertTo-Json -InputObject @{version=1;profiles=$profiles} -Depth 12),(New-Object Text.UTF8Encoding($false)))
 $phase='quoted command length'
 $nativePath=Join-Path (Split-Path -Parent $helper) 'developer\workers\task-native.cs'
 Add-Type -Path $nativePath -ErrorAction Stop
 $quotedExecutable='C:\synthetic space\fixture.exe'
 $quotedParts=[string[]]@('fixed value','quote"value','C:\end\','',([char]::ConvertFromUtf32(0x1F600)))
 $baseCommand=[Starbridge.Developer.TaskProcess]::BuildCommandLine($quotedExecutable,$quotedParts)
 $padding=32766-$baseCommand.Length-3
 $boundaryParts=[string[]](@($quotedParts)+@(('x'*$padding)))
 $boundary=[Starbridge.Developer.TaskProcess]::BuildCommandLine($quotedExecutable,$boundaryParts)
 Assert-TaskTest ($boundary.Length -eq 32766) 'Command boundary omitted UTF16 or quoting.'
 Assert-TaskTest ($boundary.ToString().StartsWith('"C:\synthetic space\fixture.exe" ')) 'Executable quoting changed.'
 Assert-TaskTest ($boundary.ToString().Contains('"quote\"value"') -and $boundary.ToString().Contains('"C:\end\\"') -and $boundary.ToString().Contains(' "" ')) 'Quoted argument expansion changed.'
 foreach($suffix in @('x',([char]::ConvertFromUtf32(0x1F600)))){
  $overParts=[string[]](@($quotedParts)+@(('x'*$padding)+$suffix));$rejected=$false
  try{$null=[Starbridge.Developer.TaskProcess]::BuildCommandLine($quotedExecutable,$overParts)}catch{$rejected=$true}
  Assert-TaskTest $rejected 'Native command length overflow was accepted.'
 }
 $tasksPath=Join-Path $root 'tasks'
 $beforeTasks=@(Get-ChildItem -LiteralPath $tasksPath -Directory -ErrorAction SilentlyContinue)
 $tooLong=Invoke-TaskTest 'task.start' @{profile='command-limit';args=@(('z'*2048))} -Write
 Assert-TaskTest (!$tooLong.ok -and $tooLong.error.code -ceq 'TASK_COMMAND_LINE_TOO_LONG') 'Complete task command length was not checked.'
 $afterTasks=@(Get-ChildItem -LiteralPath $tasksPath -Directory -ErrorAction SilentlyContinue)
 Assert-TaskTest ($beforeTasks.Count -eq $afterTasks.Count) 'Rejected command created task state.'
 $phase='startup request cleanup'
 # Fault only this isolated copy of the trusted helper: request.json is written
 # normally, then receipt creation fails while the real worker is suspended.
 $copyRoot=Join-Path $root 'helper-copy';$null=New-Item -ItemType Directory -Path $copyRoot
 Copy-Item -LiteralPath $helper -Destination (Join-Path $copyRoot 'developer-bridge.ps1')
 Copy-Item -LiteralPath (Join-Path (Split-Path -Parent $helper) 'developer') -Destination (Join-Path $copyRoot 'developer') -Recurse
 $failureModule=@'
$script:SyntheticTaskWriter=(Get-Command Write-TaskJson).ScriptBlock
function Write-TaskJson([string]$Path,$Value) {
    if([IO.Path]::GetFileName($Path) -ceq 'receipt.json') {
        $requestPath=Join-Path (Split-Path -Parent $Path) 'request.json'
        if([IO.File]::Exists($requestPath)) {
            [IO.File]::WriteAllText((Join-Path $script:DeveloperRoot 'request-observed.txt'),'observed')
        }
        throw 'Synthetic receipt write failure.'
    }
    & $script:SyntheticTaskWriter $Path $Value
}
'@
 [IO.File]::WriteAllText((Join-Path $copyRoot 'developer\zz-startup-failure.ps1'),($failureModule+[Environment]::NewLine),(New-Object Text.UTF8Encoding($false)))
 $originalHelper=$helper
 try{
  $helper=Join-Path $copyRoot 'developer-bridge.ps1'
  $failed=Invoke-TaskTest 'task.start' @{profile='echo';args=@('synthetic-sensitive-argument');env=@{STARBRIDGE_TEST_LABEL='synthetic-sensitive-environment'}} -Write
 }finally{$helper=$originalHelper}
 Assert-TaskTest (!$failed.ok -and $failed.error.code -ceq 'TASK_START_FAILED') 'Synthetic receipt failure was not returned.'
 Assert-TaskTest ([IO.File]::Exists((Join-Path $copyRoot 'request-observed.txt'))) 'Startup fixture did not write its request.'
 $previousTaskNames=@($afterTasks | ForEach-Object {$_.Name})
 $failedDirectories=@(Get-ChildItem -LiteralPath $tasksPath -Directory | Where-Object {$_.Name -notin $previousTaskNames})
 Assert-TaskTest ($failedDirectories.Count -eq 1) 'Startup fixture did not isolate one task.'
 Assert-TaskTest (![IO.File]::Exists((Join-Path $failedDirectories[0].FullName 'request.json')) -and ![IO.File]::Exists((Join-Path $failedDirectories[0].FullName 'receipt.json'))) 'Failed startup retained an unowned request.'
 $failedText=ConvertTo-Json -InputObject $failed -Depth 12 -Compress
 Assert-TaskTest (!$failedText.Contains('synthetic-sensitive-argument') -and !$failedText.Contains('synthetic-sensitive-environment')) 'Startup failure disclosed request values.'
 $phase='synchronous worker bootstrap'
 # Probe the real private worker while this test retains its pipes. This fixture
 # distinguishes a script/bootstrap failure from detached transport lifetime.
 $probeId=[Guid]::NewGuid().ToString('N');$probeDirectory=Join-Path (Join-Path $root 'tasks') $probeId
 $null=New-Item -ItemType Directory -Path $probeDirectory -Force
 $probeRequest=[ordered]@{task_id=$probeId;profile='flood';executable=$fixture;cwd=$root;args=@('flood',$root);env=@{};timeout_ms=30000;max_output_bytes=1024;show_window=$false}
 $probeReceipt=[ordered]@{task_id=$probeId;profile='flood';created_at=[DateTime]::UtcNow.ToString('o')}
 [IO.File]::WriteAllText((Join-Path $probeDirectory 'request.json'),(ConvertTo-Json -InputObject $probeRequest -Depth 12 -Compress),(New-Object Text.UTF8Encoding($false)))
 [IO.File]::WriteAllText((Join-Path $probeDirectory 'receipt.json'),(ConvertTo-Json -InputObject $probeReceipt -Depth 12 -Compress),(New-Object Text.UTF8Encoding($false)))
 $probeWorker=Join-Path (Split-Path -Parent $helper) 'developer\workers\task-worker.ps1'
 Assert-TaskTest (Test-Path -LiteralPath $probeWorker -PathType Leaf) 'Worker fixture file is missing.'
 $probeCommand="& '"+$probeWorker.Replace("'","''")+"' -TaskDirectory '"+$probeDirectory.Replace("'","''")+"'"
 $probeInfo=New-Object Diagnostics.ProcessStartInfo
 $probeInfo.FileName=$powerShell;$probeInfo.Arguments='-NoLogo -NoProfile -NonInteractive -Mta -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($probeCommand))
 $probeInfo.UseShellExecute=$false;$probeInfo.CreateNoWindow=$true;$probeInfo.RedirectStandardOutput=$true;$probeInfo.RedirectStandardError=$true
 $probe=New-Object Diagnostics.Process;$probe.StartInfo=$probeInfo
 try{
  $null=$probe.Start();$probeOut=$probe.StandardOutput.ReadToEndAsync();$probeErr=$probe.StandardError.ReadToEndAsync()
  if(!$probe.WaitForExit(45000)){try{$probe.Kill()}catch{};Assert-TaskTest $false 'Synchronous worker exceeded deadline.'}
  $probeOutput=$probeOut.GetAwaiter().GetResult();$probeDiagnostic=$probeErr.GetAwaiter().GetResult()
  if(![string]::IsNullOrWhiteSpace($probeDiagnostic)){
   # Classify only fixed parser/runtime markers, never print the captured text.
   $script:TaskError='WORKER_STDERR'
   if($probeDiagnostic.Contains('ParserError')){$script:TaskError='WORKER_PARSE_ERROR'}
   elseif($probeDiagnostic.Contains('ParameterBinding')){$script:TaskError='WORKER_PARAMETER_BINDING'}
   elseif($probeDiagnostic.Contains('CommandNotFound')){$script:TaskError='WORKER_COMMAND_NOT_FOUND'}
  }
  $probeStatePath=Join-Path $probeDirectory 'state.json'
  Assert-TaskTest ([IO.File]::Exists($probeStatePath)) 'Synchronous worker did not record state.'
  try{$probeState=ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($probeStatePath))}catch{Assert-TaskTest $false 'Synchronous worker state is invalid JSON.'}
  Remember-TaskTest ([PSCustomObject]@{ok=$true;data=$probeState})
  Assert-TaskTest ($probeState.status -ceq 'exited' -and $probeState.exit_code -eq 0) 'Synchronous worker failed its synthetic child.'
  Assert-TaskTest ([string]::IsNullOrWhiteSpace($probeOutput) -and [string]::IsNullOrWhiteSpace($probeDiagnostic)) 'Synchronous worker wrote diagnostics.'
 }finally{$probe.Dispose()}
 $phase='authorization'
 $denied=Invoke-TaskTest 'task.start' @{profile='echo'}
 Assert-TaskTest (!$denied.ok -and $denied.error.code -ceq 'WRITE_NOT_ALLOWED') 'Mutation authorization was bypassed.'
 $policy=Invoke-TaskTest 'task.start' @{profile='echo';env=@{UNLISTED='placeholder'}} -Write
 Assert-TaskTest (!$policy.ok -and $policy.error.code -ceq 'TASK_POLICY_DENIED') 'Environment allowlist was bypassed.'
 $phase='argv and output'
 $arguments=@('', 'space value', ('Unicode '+[char]0x4E2D+[char]0x6587), 'quote"value', 'C:\end\', '; & $(not-shell)')
 $id=Start-TaskTest 'echo' @{args=$arguments;env=@{STARBRIDGE_TEST_LABEL='placeholder-only'}}
 $state=Wait-TaskTest $id
 Assert-TaskTest ($state.status -ceq 'exited' -and $state.exit_code -eq 7) 'Exit code was not retained.'
 $actual=@([IO.File]::ReadAllLines((Join-Path $root 'arguments.txt')) | ForEach-Object {[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($_))})
 Assert-TaskTest ($actual.Count -eq $arguments.Count) 'Argument count changed.'
 for($index=0;$index -lt $arguments.Count;$index++){Assert-TaskTest ($actual[$index] -ceq $arguments[$index]) 'Argument quoting changed.'}
 Assert-TaskTest ([IO.File]::ReadAllText((Join-Path $root 'environment.txt')) -ceq 'placeholder-only') 'Allowed environment override was not received.'
 $output=Invoke-TaskTest 'task.output' @{task_id=$id;stream='stdout';offset=0;limit=3}
 Assert-TaskTest ($output.ok -and [Convert]::FromBase64String($output.data.data_base64).Length -eq 3 -and $output.data.next_offset -eq 3) 'Output chunk budget changed.'
 $next=Invoke-TaskTest 'task.output' @{task_id=$id;stream='stdout';offset=3;limit=65536}
 Assert-TaskTest ($next.ok -and $next.data.eof) 'Final output chunk is missing EOF.'
 $serialized=ConvertTo-Json -InputObject $state -Depth 12 -Compress
 Assert-TaskTest (!$serialized.Contains('placeholder-only')) 'Environment values appeared in task state.'
 $phase='bounded logs'
 $flood=Start-TaskTest 'flood';$state=Wait-TaskTest $flood
 Assert-TaskTest ($state.status -ceq 'exited' -and $state.stdout_bytes -eq 1024 -and $state.stderr_bytes -eq 1024 -and $state.stdout_truncated -and $state.stderr_truncated) 'Output files are not capped.'
 $phase='identity and cancellation'
 $id=Start-TaskTest 'tree';$childPath=Join-Path $root 'child.txt';$timer=[Diagnostics.Stopwatch]::StartNew()
 while(!(Test-Path -LiteralPath $childPath)){if($timer.ElapsedMilliseconds -gt 15000){throw 'Synthetic descendant did not start.'};Start-Sleep -Milliseconds 100}
 $childId=[int][IO.File]::ReadAllText($childPath);$child=[Diagnostics.Process]::GetProcessById($childId);$childStart=$child.StartTime.ToUniversalTime().Ticks;$child.Dispose()
 $receiptPath=Join-Path (Join-Path (Join-Path $root 'tasks') $id) 'receipt.json';$original=[IO.File]::ReadAllText($receiptPath)
 $restoreReceipts[$receiptPath]=$original
 $receipt=ConvertFrom-Json -InputObject $original;$receipt.worker.start_ticks=1
 [IO.File]::WriteAllText($receiptPath,(ConvertTo-Json -InputObject $receipt -Depth 12),(New-Object Text.UTF8Encoding($false)))
 $blocked=Invoke-TaskTest 'task.cancel' @{task_id=$id} -Write
 Assert-TaskTest ($blocked.ok -and !$blocked.data.accepted -and $blocked.data.status -ceq 'orphaned') 'Stale process identity was accepted.'
 [IO.File]::WriteAllText($receiptPath,$original,(New-Object Text.UTF8Encoding($false)));$restoreReceipts.Remove($receiptPath)
 $cancel=Invoke-TaskTest 'task.cancel' @{task_id=$id} -Write
 Assert-TaskTest ($cancel.ok) 'Cancellation request failed.'
 $state=Wait-TaskTest $id;Assert-TaskTest ($state.status -ceq 'cancelled') 'Task was not cancelled.'
 $descendantAlive=$false
 try{$child=[Diagnostics.Process]::GetProcessById($childId);$descendantAlive=(!$child.HasExited -and $child.StartTime.ToUniversalTime().Ticks -eq $childStart);$child.Dispose()}catch [ArgumentException]{}
 Assert-TaskTest (!$descendantAlive) 'Cancellation left an owned descendant running.'
 $phase='timeout'
 Remove-Item -LiteralPath $childPath -ErrorAction SilentlyContinue
 $timed=Start-TaskTest 'timeout';$state=Wait-TaskTest $timed
 Assert-TaskTest ($state.status -ceq 'timed_out') 'Configured deadline did not end the task.'
 $phase='normal close'
 $id=Start-TaskTest 'sleep'
 if([Environment]::UserInteractive -and [Diagnostics.Process]::GetCurrentProcess().SessionId -gt 0){
  $response=Invoke-TaskTest 'task.close' @{task_id=$id} -Write
  Assert-TaskTest ($response.ok -and !$response.data.accepted -and $response.data.control_status -ceq 'blocked') 'No-window close did not report blocked.'
  $gui=Start-TaskTest 'gui';Start-Sleep -Seconds 2
  $response=Invoke-TaskTest 'task.close' @{task_id=$gui} -Write
  Assert-TaskTest ($response.ok -and $response.data.accepted) 'Synthetic window did not accept normal close.'
  $state=Wait-TaskTest $gui;Assert-TaskTest ($state.status -ceq 'exited' -and $state.exit_code -eq 0) 'Normal close did not exit cleanly.'
 }else{
  $response=Invoke-TaskTest 'task.close' @{task_id=$id} -Write
  Assert-TaskTest (!$response.ok -and $response.error.code -ceq 'INTERACTIVE_SESSION_REQUIRED') 'Noninteractive close was not explicit.'
 }
 Write-Host 'PASS: configured task authorization, command length, startup cleanup, argv/env, bounded chunks, exit code, process identity, tree cancellation, timeout and close contracts.'
}catch{
 # Do not print runtime paths, environment values, or raw child output.
 $type=$_.Exception.GetType().Name;if($type -cnotmatch '^[A-Za-z0-9_.]{1,128}$'){$type='unknown'}
 $line=$_.InvocationInfo.ScriptLineNumber
 [Console]::Error.WriteLine(('Developer task fixture failed: phase={0}; assertion={1}; error={2}; status={3}; worker_stage={4}; worker_type={5}; worker_line={6}; native_stage={7}; native_error={8}; test_type={9}; test_line={10}; worker_identity={11}; identity_type={12}; identity_native={13}; identity_delta={14}.' -f $phase,$script:TaskAssertion,$script:TaskError,$script:TaskStatus,$script:TaskWorkerStage,$script:TaskWorkerType,$script:TaskWorkerLine,$script:TaskNativeStage,$script:TaskNativeError,$type,$line,$script:TaskIdentity,$script:TaskIdentityType,$script:TaskIdentityNative,$script:TaskIdentityDelta))
 exit 1
}finally{
 foreach($path in @($restoreReceipts.Keys)){try{[IO.File]::WriteAllText($path,$restoreReceipts[$path],(New-Object Text.UTF8Encoding($false)))}catch{}}
 foreach($id in $ids){try{$null=Invoke-TaskTest 'task.cancel' @{task_id=$id} -Write;$null=Wait-TaskTest $id}catch{}}
 $env:STARBRIDGE_DEV_HOME=$previous
 try{Remove-Item -LiteralPath $root -Recurse -Force}catch{}
}
