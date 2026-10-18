#requires -Version 5.1
<# Shared synthetic developer checks for normal CI, release gates and local use.
Each selected contract runs in its own Windows PowerShell process. Contracts
use temporary data/processes; task checks may create and close their own GUI.
Opt-in desktop/VM acceptance scripts are deliberately not selected. #>
[CmdletBinding()]
param()

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).ProviderPath
$contracts = @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter 'developer-*.ps1' -File |
    Where-Object { $_.Name -match '^developer-(contract|tasks|files|bootstrap|.+-contract)\.ps1$' } |
    Sort-Object Name)
if ($contracts.Count -eq 0) { throw 'No synthetic developer contracts were found.' }

$powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if ([Environment]::Is64BitOperatingSystem -and ![Environment]::Is64BitProcess) {
    $powerShell = Join-Path $env:SystemRoot 'Sysnative\WindowsPowerShell\v1.0\powershell.exe'
}
if (!(Test-Path -LiteralPath $powerShell -PathType Leaf)) { throw 'Windows PowerShell is required for developer contracts.' }

Push-Location -LiteralPath $root
try {
    foreach ($contract in $contracts) {
        Write-Host ('Running synthetic contract: ' + $contract.Name)
        $command = "& '" + $contract.FullName.Replace("'", "''") + "'"
        $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
        & $powerShell -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand $encoded
        if ($LASTEXITCODE -ne 0) { throw ('Synthetic contract failed: ' + $contract.Name + ' (exit code ' + $LASTEXITCODE + ').') }
    }
} finally {
    Pop-Location
}
Write-Host ('Developer synthetic contracts passed: ' + $contracts.Count)
