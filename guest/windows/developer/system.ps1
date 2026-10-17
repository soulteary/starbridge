# Protocol and installed-code identity contain no usernames or guest paths.
function Get-DevHelperHash {
    $sha = [Security.Cryptography.SHA256]::Create()
    $records = New-Object 'Collections.Generic.List[string]'
    try {
        $files = @((Get-Item -LiteralPath $script:DeveloperScriptPath)) +
            @(Get-ChildItem -LiteralPath (Join-Path $script:DeveloperRoot 'developer') -Recurse -File | Where-Object { $_.Extension -in @('.ps1', '.cs') })
        foreach ($file in $files) {
            $relative = $file.FullName.Substring($script:DeveloperRoot.Length).TrimStart('\', '/').Replace('\', '/')
            $stream = [IO.File]::OpenRead($file.FullName)
            try { $hash = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
            finally { $stream.Dispose() }
            $records.Add($relative + [char]0 + $hash + "`n")
        }
        $records.Sort([StringComparer]::Ordinal)
        $bytes = [Text.Encoding]::UTF8.GetBytes([string]::Join('', $records.ToArray()))
        return [BitConverter]::ToString($sha.ComputeHash($bytes)).Replace('-', '').ToLowerInvariant()
    } finally { $sha.Dispose() }
}

Register-DeveloperAction -Name 'system.info' -Description 'Inspect the Windows environment, session and installed developer actions.' -Handler {
    param($p)
    Assert-DevFields $p @()
    $capabilities = @($script:DevActions.Keys | Sort-Object | ForEach-Object {
        $registration = $script:DevActions[$_]
        [ordered]@{ name=$_; mutation=$registration.mutation; interactive=$registration.interactive; description=$registration.description }
    })
    $osArchitecture = 'x86'
    if ([Environment]::Is64BitOperatingSystem) { $osArchitecture = 'x64' }
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $osArchitecture = 'arm64' }
    $processArchitecture = $env:PROCESSOR_ARCHITECTURE.ToLowerInvariant()
    return [ordered]@{
        os = [ordered]@{ name='Windows'; version=[Environment]::OSVersion.Version.ToString(); build=[string][Environment]::OSVersion.Version.Build }
        architecture = [ordered]@{ os=$osArchitecture; process=$processArchitecture }
        powershell_version = $PSVersionTable.PSVersion.ToString()
        dotnet_version = [Environment]::Version.ToString()
        session = [ordered]@{ id=[Diagnostics.Process]::GetCurrentProcess().SessionId; user_interactive=[Environment]::UserInteractive }
        helper = [ordered]@{ protocol_version='dev-1'; version='dev-1'; sha256=(Get-DevHelperHash) }
        capabilities = @($capabilities)
    }
} -Mutation:$false -Interactive:$false
