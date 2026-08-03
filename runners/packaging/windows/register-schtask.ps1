# Register (or refresh) the wuling-runner scheduled task.
# Invoked by the Inno Setup installer when the "schtask" task is selected.
$ErrorActionPreference = 'Stop'

$runCmd = Join-Path $PSScriptRoot 'run.cmd'
$action = New-ScheduledTaskAction -Execute $runCmd
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries `
  -DontStopIfGoingOnBatteries `
  -RestartCount 999 `
  -RestartInterval (New-TimeSpan -Minutes 1) `
  -ExecutionTimeLimit (New-TimeSpan -Seconds 0)

Register-ScheduledTask `
  -TaskName 'wuling-runner' `
  -Action $action `
  -Trigger $trigger `
  -Principal $principal `
  -Settings $settings `
  -Force | Out-Null
