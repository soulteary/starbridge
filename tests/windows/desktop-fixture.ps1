#requires -Version 5.1
<# A visible, disposable test window. Run only in a logged-in test desktop. #>
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$MetadataPath)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[Windows.Forms.Application]::EnableVisualStyles()
$form = New-Object Windows.Forms.Form
$form.Text = 'Starbridge desktop fixture'
$form.Size = New-Object Drawing.Size(500,220)
$button = New-Object Windows.Forms.Button
$button.Name = 'StarbridgeAcceptanceButton'
$button.Text = 'Starbridge ' + [char]0x4E2D + [char]0x6587
$button.AccessibleName = $button.Text
$button.Location = New-Object Drawing.Point(25,30)
$button.Size = New-Object Drawing.Size(250,60)
$form.Controls.Add($button)
$form.Add_Shown({
    $metadata = [ordered]@{
        pid = $PID
        hwnd = '0x' + $form.Handle.ToInt64().ToString('X')
        button_name = $button.Text
        title = $form.Text
    }
    $json = ConvertTo-Json -InputObject $metadata -Compress
    $temporaryPath = $MetadataPath + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    [IO.File]::WriteAllText($temporaryPath,$json,(New-Object Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $temporaryPath -Destination $MetadataPath -Force
})
try { [Windows.Forms.Application]::Run($form) }
finally { $form.Dispose() }
