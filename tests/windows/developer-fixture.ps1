#requires -Version 5.1
# A synthetic application used only in an explicitly provisioned test VM.
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[Windows.Forms.Application]::EnableVisualStyles()
$form = New-Object Windows.Forms.Form
$form.Text = 'Starbridge developer fixture'
$form.Size = New-Object Drawing.Size(480,240)
$counter = New-Object Windows.Forms.Label
$counter.Name = 'StarbridgeSyntheticCounter'
$counter.Text = '0'
$counter.Location = New-Object Drawing.Point(25,95)
$button = New-Object Windows.Forms.Button
$button.Name = 'StarbridgeSyntheticIncrement'
$button.Text = 'Starbridge ' + [char]0x4E2D + [char]0x6587
$button.AccessibleName = $button.Text
$button.Location = New-Object Drawing.Point(25,25)
$button.Size = New-Object Drawing.Size(240,50)
$button.Add_Click({ $counter.Text = ([int]$counter.Text + 1).ToString() })
$editor = New-Object Windows.Forms.TextBox
$editor.Name = 'StarbridgeSyntheticEditor'
$editor.AccessibleName = 'Synthetic editor'
$editor.Location = New-Object Drawing.Point(25,130)
$editor.Size = New-Object Drawing.Size(300,25)
$form.Controls.AddRange(@($button,$counter,$editor))
$form.Add_Shown({
    $process = [Diagnostics.Process]::GetCurrentProcess()
    try {
        $metadata = @{pid=$PID;hwnd=('0x'+$form.Handle.ToInt64().ToString('x'));start_time_utc=$process.StartTime.ToUniversalTime().ToString('o')}
        [Console]::Out.WriteLine((ConvertTo-Json -InputObject $metadata -Compress))
        [Console]::Out.Flush()
    } finally { $process.Dispose() }
})
try { [Windows.Forms.Application]::Run($form) } finally { $form.Dispose() }
