<#
.SYNOPSIS
  Install the prebuilt wuling-runner.exe + a Scheduled Task into a Windows
  runner image (Server 2022 or newer).

.DESCRIPTION
  Downloads a release artifact — NO Rust toolchain is installed, keeping the
  image small. Registers a Scheduled Task `wuling-runner` (running as SYSTEM)
  rather than a Windows service, because the runner is a plain console binary
  and a task needs no third-party service shim (NSSM/WinSW).

  The task runs a wrapper (run.cmd) that loads per-instance configuration from
  C:\ProgramData\wuling-runner\runner.env — written by the autoscaler's
  user-data (see internal/autoscale/cloudinit.go) or by hand for a static
  runner. The image bakes in NO token.

.EXAMPLE
  $env:WULING_RUNNER_VERSION = 'v0.3.0'; ./setup.ps1
#>
#requires -RunAsAdministrator
$ErrorActionPreference = 'Stop'

$repo = if ($env:WULING_REPO) { $env:WULING_REPO } else { 'zixiao-labs/Wuling-DevOps' }
$version = $env:WULING_RUNNER_VERSION
if (-not $version) { throw 'set WULING_RUNNER_VERSION to a release tag, e.g. v0.3.0' }

$dir = 'C:\wuling-runner'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
New-Item -ItemType Directory -Force -Path 'C:\ProgramData\wuling-runner' | Out-Null

# amd64 is the only published Windows architecture today.
$url = "https://github.com/$repo/releases/download/$version/wuling-runner-windows-amd64.zip"
$zip = Join-Path $env:TEMP 'wuling-runner.zip'
Write-Host "Downloading $url"
Invoke-WebRequest -Uri $url -OutFile $zip
Invoke-WebRequest -Uri "$url.sha256" -OutFile "$zip.sha256"
$want = ((Get-Content "$zip.sha256") -split '\s+')[0].Trim().ToLower()
$got = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
if ($want -ne $got) { throw "checksum mismatch: want $want got $got" }
Expand-Archive -Path $zip -DestinationPath $dir -Force
Remove-Item $zip, "$zip.sha256"

# Wrapper: load runner.env (KEY=VALUE lines, '#' comments) into the environment,
# then exec the runner. Keeping config in the file (not the task definition)
# means the autoscaler can write per-instance token/labels at boot.
$wrapper = @'
@echo off
setlocal enabledelayedexpansion
set "ENVFILE=C:\ProgramData\wuling-runner\runner.env"
if exist "%ENVFILE%" (
  for /f "usebackq eol=# tokens=1,* delims==" %%a in ("%ENVFILE%") do set "%%a=%%b"
)
"C:\wuling-runner\wuling-runner.exe"
'@
Set-Content -Path (Join-Path $dir 'run.cmd') -Value $wrapper -Encoding ascii

# Register as SYSTEM, highest privileges, restart-on-exit, no time limit. The
# AtStartup trigger covers reboots; the autoscaler also triggers it explicitly
# with `schtasks /Run` after writing runner.env on first boot.
$action = New-ScheduledTaskAction -Execute 'C:\wuling-runner\run.cmd'
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
  -ExecutionTimeLimit (New-TimeSpan -Seconds 0)
Register-ScheduledTask -TaskName 'wuling-runner' -Action $action -Trigger $trigger `
  -Principal $principal -Settings $settings -Force | Out-Null

Write-Host "Installed C:\wuling-runner\wuling-runner.exe and the 'wuling-runner' Scheduled Task."
Write-Host ""
Write-Host "Do NOT start it in the image — the autoscaler writes runner.env and runs the task per instance."
Write-Host ""
Write-Host "For a MANUAL static runner, mint a registration token in the org UI, then write"
Write-Host "C:\ProgramData\wuling-runner\runner.env with:"
Write-Host "  WULING_RUNNER_SERVER_URL=https://wuling.example.com"
Write-Host "  WULING_RUNNER_REGISTRATION_TOKEN=wlreg_..."
Write-Host "and run: schtasks /Run /TN wuling-runner"
