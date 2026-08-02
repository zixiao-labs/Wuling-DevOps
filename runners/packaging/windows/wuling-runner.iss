; Inno Setup script for the Wuling CI runner (Windows amd64).
; Built by .github/workflows/release.yml after cargo --release.
;
; Defines (passed via /D on the ISCC command line):
;   MyAppVersion   — release tag, e.g. v0.3.0
;   RunnerExe      — absolute path to wuling-runner.exe
;   OutDir         — directory for the compiled installer

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0-dev"
#endif
#ifndef RunnerExe
  #define RunnerExe "..\..\runner-clients\target\release\wuling-runner.exe"
#endif
#ifndef OutDir
  #define OutDir "out"
#endif

#define MyAppName "Wuling Runner"
#define MyAppPublisher "Zixiao Laboratory"
#define MyAppURL "https://github.com/zixiao-labs/Wuling-DevOps"

[Setup]
AppId={{A7C8E2F1-4B3D-4E9A-9C1F-2D6B8A0E5F73}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
DefaultDirName=C:\wuling-runner
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=admin
OutputDir={#OutDir}
OutputBaseFilename=wuling-runner-windows-amd64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayIcon={app}\wuling-runner.exe

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "{#RunnerExe}"; DestDir: "{app}"; DestName: "wuling-runner.exe"; Flags: ignoreversion
Source: "run.cmd"; DestDir: "{app}"; Flags: ignoreversion

[Dirs]
Name: "C:\ProgramData\wuling-runner"; Permissions: admins-full system-full

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\wuling-runner.exe"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"

[Tasks]
Name: "schtask"; Description: "Register Scheduled Task ""wuling-runner"" (runs at startup as SYSTEM)"; Flags: checkedonce

[Run]
; Register the scheduled task after files are in place. Safe to re-run on upgrade.
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -ExecutionPolicy Bypass -Command ""\
    $action = New-ScheduledTaskAction -Execute '{app}\run.cmd'; \
    $trigger = New-ScheduledTaskTrigger -AtStartup; \
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest; \
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries \
      -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) \
      -ExecutionTimeLimit (New-TimeSpan -Seconds 0); \
    Register-ScheduledTask -TaskName 'wuling-runner' -Action $action -Trigger $trigger \
      -Principal $principal -Settings $settings -Force | Out-Null\
  """; \
  StatusMsg: "Registering scheduled task…"; \
  Flags: runhidden; \
  Tasks: schtask

[UninstallRun]
Filename: "schtasks.exe"; Parameters: "/Delete /TN wuling-runner /F"; Flags: runhidden; RunOnceId: "RemoveSchTask"

[Code]
function InitializeSetup(): Boolean;
begin
  Result := True;
end;
